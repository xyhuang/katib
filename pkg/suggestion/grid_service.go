package suggestion

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/kubeflow/katib/pkg"
	"github.com/kubeflow/katib/pkg/api"

	"google.golang.org/grpc"
)

type GridSuggestParameters struct {
	defaultGridNum int
	gridConfig     map[string]int
	MaxParallel    int
}

type GridSuggestService struct {
}

func NewGridSuggestService() *GridSuggestService {
	return &GridSuggestService{}
}

func (s *GridSuggestService) allocInt(min int, max int, reqnum int) []string {
	ret := make([]string, reqnum)
	if reqnum == 1 {
		ret[0] = strconv.Itoa(min)
	} else {
		for i := 0; i < reqnum; i++ {
			ret[i] = strconv.Itoa(min + ((max - min) * i / (reqnum - 1)))
		}
	}
	return ret
}

func (s *GridSuggestService) allocFloat(min float64, max float64, reqnum int) []string {
	ret := make([]string, reqnum)
	if reqnum == 1 {
		ret[0] = strconv.FormatFloat(min, 'f', 4, 64)
	} else {
		for i := 0; i < reqnum; i++ {
			ret[i] = strconv.FormatFloat(min+(((max-min)/float64(reqnum-1))*float64(i)), 'f', 4, 64)
		}
	}
	return ret
}

func (s *GridSuggestService) allocCat(list []string, reqnum int) []string {
	ret := make([]string, reqnum)
	if reqnum == 1 {
		ret[0] = list[0]
	} else {
		for i := 0; i < reqnum; i++ {
			ret[i] = list[(((len(list) - 1) * i) / (reqnum - 1))]
			fmt.Printf("ret %v %v\n", i, ret[i])
		}
	}
	return ret
}

func (s *GridSuggestService) setP(gci int, p [][]*api.Parameter, pg [][]string, pcs []*api.ParameterConfig) {
	if gci == len(pg)-1 {
		for i := range pg[gci] {
			p[i] = append(p[i], &api.Parameter{
				Name:          pcs[gci].Name,
				ParameterType: pcs[gci].ParameterType,
				Value:         pg[gci][i],
			})

		}
		return
	} else {
		d := len(p) / len(pg[gci])
		for i := range pg[gci] {
			for j := d * i; j < d*(i+1); j++ {
				p[j] = append(p[j], &api.Parameter{
					Name:          pcs[gci].Name,
					ParameterType: pcs[gci].ParameterType,
					Value:         pg[gci][i],
				})
			}
			s.setP(gci+1, p[d*i:d*(i+1)], pg, pcs)
		}
	}
}

func (s *GridSuggestService) parseSuggestParam(suggestParam []*api.SuggestionParameter) (int, int, map[string]int) {
	ret := make(map[string]int)
	defaultGrid := 0
	i := 0
	for _, sp := range suggestParam {
		switch sp.Name {
		case "DefaultGrid":
			defaultGrid, _ = strconv.Atoi(sp.Value)
		case "Iteration":
			i, _ = strconv.Atoi(sp.Value)
		default:
			ret[sp.Name], _ = strconv.Atoi(sp.Value)
		}
	}
	if defaultGrid <= 0 {
		defaultGrid = 1
	}
	return defaultGrid, i, ret
}
func (s *GridSuggestService) genGrids(studyID string, pcs []*api.ParameterConfig, df int, glist map[string]int) [][]*api.Parameter {
	var pg [][]string
	var holenum = 1
	gcl := make([]int, len(pcs))
	for i, pc := range pcs {
		gc, ok := glist[pc.Name]
		if !ok {
			gc = df
		}
		holenum *= gc
		gcl[i] = gc
		switch pc.ParameterType {
		case api.ParameterType_INT:
			imin, _ := strconv.Atoi(pc.Feasible.Min)
			imax, _ := strconv.Atoi(pc.Feasible.Max)
			pg = append(pg, s.allocInt(imin, imax, gc))
		case api.ParameterType_DOUBLE:
			dmin, _ := strconv.ParseFloat(pc.Feasible.Min, 64)
			dmax, _ := strconv.ParseFloat(pc.Feasible.Max, 64)
			pg = append(pg, s.allocFloat(dmin, dmax, gc))
		case api.ParameterType_CATEGORICAL:
			pg = append(pg, s.allocCat(pc.Feasible.List, gc))
		}
	}
	ret := make([][]*api.Parameter, holenum)
	s.setP(0, ret, pg, pcs)
	log.Printf("Study %v : %v parameters generated", studyID, holenum)
	return ret
}

func (s *GridSuggestService) GetSuggestions(ctx context.Context, in *api.GetSuggestionsRequest) (*api.GetSuggestionsReply, error) {
	conn, err := grpc.Dial(pkg.ManagerAddr, grpc.WithInsecure())
	if err != nil {
		log.Printf("could not connect: %v", err)
		return &api.GetSuggestionsReply{}, err
	}
	defer conn.Close()
	c := api.NewManagerClient(conn)
	screq := &api.GetStudyRequest{
		StudyId: in.StudyId,
	}
	scr, err := c.GetStudy(ctx, screq)
	if err != nil {
		log.Printf("GetStudyConf failed: %v", err)
		return &api.GetSuggestionsReply{}, err
	}
	spreq := &api.GetSuggestionParametersRequest{
		ParamId: in.ParamId,
	}
	spr, err := c.GetSuggestionParameters(ctx, spreq)
	if err != nil {
		log.Printf("GetParameter failed: %v", err)
		return &api.GetSuggestionsReply{}, err
	}
	df, iteration, glist := s.parseSuggestParam(spr.SuggestionParameters)
	log.Printf("Study %s iteration %d DefaltGrid %d Grids %v", in.StudyId, iteration, df, glist)
	grids := s.genGrids(in.StudyId, scr.StudyConfig.ParameterConfigs.Configs, df, glist)
	var reqnum = int(in.RequestNumber)
	if reqnum == 0 {
		reqnum = len(grids)
	}
	if iteration+reqnum > len(grids) {
		reqnum = len(grids) - iteration
	}
	trials := make([]*api.Trial, reqnum)
	if reqnum <= 0 {
		return &api.GetSuggestionsReply{Trials: []*api.Trial{}}, err
	}
	for i := 0; i < int(reqnum); i++ {
		trials[i] = &api.Trial{
			StudyId:      in.StudyId,
			ParameterSet: grids[iteration+i],
		}
	}
	iteration += reqnum
	sspreq := &api.SetSuggestionParametersRequest{
		StudyId:             in.StudyId,
		SuggestionAlgorithm: in.SuggestionAlgorithm,
		ParamId:             in.ParamId,
	}
	sp := []*api.SuggestionParameter{}
	sp = append(sp, &api.SuggestionParameter{
		Name:  "DefaultGrid",
		Value: strconv.Itoa(df),
	})
	sp = append(sp, &api.SuggestionParameter{
		Name:  "Iteration",
		Value: strconv.Itoa(iteration),
	})
	for gn, gv := range glist {
		sp = append(sp, &api.SuggestionParameter{
			Name:  gn,
			Value: strconv.Itoa(gv),
		})
	}
	sspreq.SuggestionParameters = sp
	_, err = c.SetSuggestionParameters(ctx, sspreq)
	if err != nil {
		log.Printf("Save Parameters Failed %v", err)
		return &api.GetSuggestionsReply{}, err
	}
	for i, t := range trials {
		req := &api.CreateTrialRequest{
			Trial: t,
		}
		ret, err := c.CreateTrial(ctx, req)
		if err != nil {
			return &api.GetSuggestionsReply{Trials: []*api.Trial{}}, err
		}
		trials[i].TrialId = ret.TrialId
	}
	return &api.GetSuggestionsReply{Trials: trials}, nil
}
