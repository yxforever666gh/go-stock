package data

import (
	"github.com/go-resty/resty/v2"
	"testing"
)

// @Author spark
// @Date 2025/1/3 13:53
// @Desc
//-----------------------------------------------------------------------------------

func TestRobot(t *testing.T) {
	requireIntegration(t)
	dingdingRobotUrl := "XXX"
	resp, err := resty.New().R().
		SetHeader("Content-Type", "application/json").
		SetBody(`{
		  "msgtype": "markdown",
			 "markdown": {
				 "title":"go-stock",
				 "text": "#### 杭州天气 @150XXXXXXXX \n > 9度，西北风1级，空气良89，相对温度73%\n > ![screenshot](https://img.alicdn.com/tfs/TB1NwmBEL9TBuNjy1zbXXXpepXa-2400-1218.png)\n > ###### 10点20分发布 [天气](https://www.dingtalk.com) \n"
			 },
		  "at": {
			"isAtAll": true
		  }
		}`).
		Post(dingdingRobotUrl)
	if err != nil {
		t.Error(err)
	}
	t.Log(resp.String())
}
