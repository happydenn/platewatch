package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	api2captcha "github.com/2captcha/2captcha-go"
	"github.com/PuerkitoBio/goquery"
	"github.com/imroc/req/v3"
	"github.com/robfig/cron/v3"
)

var solver *api2captcha.Client

func init2CaptchaClient(k string) {
	solver = api2captcha.NewClient(k)
	solver.PollingInterval = 5
}

type Plate struct {
	Number string `json:"number"`
}

func queryPlates(pattern string) ([]Plate, error) {
	client := req.C()
	client.SetTimeout(5 * time.Second)

	tryCount := 0

	for tryCount < 3 {
		landingResp, err := client.R().Get("https://www.mvdis.gov.tw/m3-emv-plate/webpickno/queryPickNo")
		if err != nil {
			return nil, err
		}

		landingDoc, err := goquery.NewDocumentFromReader(bytes.NewReader(landingResp.Bytes()))
		if err != nil {
			return nil, err
		}

		csrfToken, found := landingDoc.Find("input[name='CSRFToken']").First().Attr("value")
		if !found {
			return nil, errors.New("csrf token not found")
		}

		log.Printf("csrf token=%s", csrfToken)

		capResp, err := client.R().Get("https://www.mvdis.gov.tw/m3-emv-plate/captchaImg.jpg")
		if err != nil {
			return nil, err
		}

		n := api2captcha.Normal{
			Base64: base64.StdEncoding.EncodeToString(capResp.Bytes()),
			MinLen: 4,
			MaxLen: 4,
		}
		ans, err := solver.Solve(n.ToRequest())
		if err != nil {
			return nil, err
		}

		log.Printf("captcha answer=%s", ans)

		formData := map[string]any{
			"method":         "qryPickNo",
			"selDeptCode":    2,
			"selStationCode": 20,
			"selWindowNo":    "01",
			"location":       "臺北市八德路4段21號地下室",
			"selCarType":     "C",
			"selEnergyType":  "C",
			"selPlateType":   2,
			"plateVer":       2,
			"validateStr":    strings.ToUpper(ans),
			"queryType":      2,
			"queryNo":        pattern,
			"CSRFToken":      csrfToken,
		}

		queryResp, err := client.R().SetFormDataAnyType(formData).Post("https://www.mvdis.gov.tw/m3-emv-plate/webpickno/queryPickNo")
		if err != nil {
			return nil, err
		}

		queryDoc, err := goquery.NewDocumentFromReader(bytes.NewReader(queryResp.Bytes()))
		if err != nil {
			return nil, err
		}

		if queryDoc.Find(":contains('驗證數字輸入錯誤')").Length() > 0 {
			log.Print("incorrect captcha")
			tryCount += 1
			continue
		}

		var plates []Plate
		queryDoc.Find("#countList .number_cell .number").Each(func(i int, s *goquery.Selection) {
			plates = append(plates, Plate{s.Text()})
		})

		log.Printf("%d plates found", len(plates))
		return plates, nil
	}

	return nil, errors.New("maximum retries reached")
}

func checkPlates(pat, topic string) {
	var lastRes []Plate

	lf, err := os.ReadFile("last.json")
	if err == nil {
		json.Unmarshal(lf, &lastRes)
	}

	log.Printf("last=%+v", lastRes)

	res, err := queryPlates(pat)
	if err != nil {
		log.Printf("queryPlates error: %v", err)
		return
	}

	log.Printf("current=%+v", res)

	cf, _ := json.Marshal(res)
	os.WriteFile("last.json", cf, 0666)

	chk := map[Plate]bool{}
	for _, p := range res {
		chk[p] = true
	}

	for _, p := range lastRes {
		delete(chk, p)
	}

	var newPlates []Plate
	for p := range chk {
		newPlates = append(newPlates, p)
	}

	sort.Slice(newPlates, func(i, j int) bool {
		return newPlates[i].Number < newPlates[j].Number
	})

	log.Printf("new=%+v", newPlates)

	if len(newPlates) > 0 && topic != "" {
		log.Printf("sending notification to %s", topic)

		var plateNumbers []string
		for _, p := range newPlates {
			plateNumbers = append(plateNumbers, p.Number)
		}

		msg := fmt.Sprintf("新車牌釋出: %s", strings.Join(plateNumbers, ", "))
		http.Post(fmt.Sprintf("https://ntfy.sh/%s", topic), "text/plain", strings.NewReader(msg))
	}
}

func main() {
	apiKey := os.Getenv("TWOCAPTCHA_API_KEY")
	if apiKey == "" {
		log.Fatal("TWOCAPTCHA_API_KEY is required")
	}

	init2CaptchaClient(apiKey)

	pat := os.Getenv("PLATE_PATTERN")
	if pat == "" {
		log.Fatal("PLATE_PATTERN is required")
	}

	intvl := os.Getenv("CHECK_INTERVAL")
	if intvl == "" {
		intvl = "5m"
	}

	topic := os.Getenv("NTFY_TOPIC")

	c := cron.New()

	checkFunc := func() { checkPlates(pat, topic) }
	c.AddFunc(fmt.Sprintf("@every %s", intvl), checkFunc)

	log.Printf("Monitoring for new plates matching %s every %s", pat, intvl)
	c.Start()
	go checkFunc()

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
	<-done
}
