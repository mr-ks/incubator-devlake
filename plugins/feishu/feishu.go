package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/faabiosr/cachego/file"
	"github.com/fastwego/feishu"
	"github.com/merico-dev/lake/config"
	"github.com/merico-dev/lake/logger"
	lakeModels "github.com/merico-dev/lake/models"
	"github.com/merico-dev/lake/plugins/core"
	"github.com/merico-dev/lake/plugins/feishu/apimodels"
	"github.com/merico-dev/lake/plugins/feishu/models"
	"github.com/merico-dev/lake/utils"
)

var _ core.Plugin = (*Feishu)(nil)

type Feishu string

func (plugin Feishu) Description() string {
	return "To collect and enrich data from Feishu"
}

func (plugin Feishu) Init() {
	logger.Info("INFO >>> init Feishu plugin", true)
	err := lakeModels.Db.AutoMigrate(
		&models.FeishuMeetingTopUserItem{},
	)
	if err != nil {
		logger.Error("Error migrating feishu: ", err)
		panic(err)
	}
}

func (plugin Feishu) Execute(options map[string]interface{}, progress chan<- float32, ctx context.Context) error {
	logger.Print("start feishu plugin execution")

	// how long do you want to collect
	numOfDaysToCollect, ok := options["numOfDaysToCollect"]
	if !ok {
		return fmt.Errorf("numOfDaysToCollect is invalid")
	}
	numOfDaysToCollectInt := int(numOfDaysToCollect.(float64))

	// tenant_access_token manager
	atm := &feishu.DefaultAccessTokenManager{
		Id:    `cli_a074eb7697f8d00b`,
		Cache: file.New(os.TempDir()),
		GetRefreshRequestFunc: func() *http.Request {
			payload := `{
                "app_id":"` + config.GetConfig().GetString("FEISHU_APPID") + `",
                "app_secret":"` + config.GetConfig().GetString("FEISHU_APPSCRECT") + `"
            }`
			req, _ := http.NewRequest(http.MethodPost, feishu.ServerUrl+"/open-apis/auth/v3/tenant_access_token/internal/", strings.NewReader(payload))
			return req
		},
	}

	rateLimitPerSecondInt, err := core.GetRateLimitPerSecond(options, 5)
	if err != nil {
		return err
	}

	scheduler, err := utils.NewWorkerScheduler(10, rateLimitPerSecondInt, ctx)
	if err != nil {
		logger.Error("could not create scheduler", false)
	}

	// create feishu client
	FeishuClient := feishu.NewClient()

	progress <- 0

	// request AccessToken api
	tenantAccessToken, err := atm.GetAccessToken()
	if err != nil {
		return err
	}
	progress <- 0.1

	err = lakeModels.Db.Delete(models.FeishuMeetingTopUserItem{}, "1=1").Error
	if err != nil {
		return err
	}
	progress <- 0.2

	endDate := time.Now()
	endDate = endDate.Truncate(24 * time.Hour)
	startDate := endDate.AddDate(0, 0, -1)
	progress <- 0.3
	baseProgress := float32(0)
	for i := 0; i < numOfDaysToCollectInt; i++ {
		baseProgress = float32(i) / float32(numOfDaysToCollectInt)
		progress <- baseProgress*0.7 + 0.3
		params := url.Values{}
		params.Add(`start_time`, strconv.FormatInt(startDate.Unix(), 10))
		params.Add(`end_time`, strconv.FormatInt(endDate.Unix(), 10))
		params.Add(`limit`, `100`)
		params.Add(`order_by`, `2`)
		endDate = startDate
		startDate = endDate.AddDate(0, 0, -1)

		tempStartDate := startDate
		err := scheduler.Submit(func() error {
			request, _ := http.NewRequest(http.MethodGet, feishu.ServerUrl+"/open-apis/vc/v1/reports/get_top_user?"+params.Encode(), nil)
			resp, err := FeishuClient.Do(request, tenantAccessToken)
			if err != nil {
				return fmt.Errorf("Current api req is "+params.Get(`start_time`)+";", err)
			}

			var result apimodels.FeishuMeetingTopUserItemResult
			err = json.Unmarshal(resp, &result)
			if err != nil {
				return err
			}

			for index := range result.Data.TopUserReport {
				item := &result.Data.TopUserReport[index]
				item.StartTime = tempStartDate
			}
			err = lakeModels.Db.Save(result.Data.TopUserReport).Error
			return err
		})
		if err != nil {
			return err
		}
	}
	scheduler.WaitUntilFinish()
	progress <- 1
	return nil
}

func (plugin Feishu) RootPkgPath() string {
	return "github.com/merico-dev/lake/plugins/feishu"
}

func (plugin Feishu) ApiResources() map[string]map[string]core.ApiResourceHandler {
	return map[string]map[string]core.ApiResourceHandler{}
}

var PluginEntry Feishu

// standalone mode for debugging
func main() {
	PluginEntry.Init()
	progress := make(chan float32)
	go func() {
		err := PluginEntry.Execute(
			map[string]interface{}{
				"numOfDaysToCollect": 80,
			},
			progress,
			context.Background(),
		)
		if err != nil {
			panic(err)
		}
	}()
	for p := range progress {
		fmt.Println(p)
	}
}
