/*
  Created on Thu Oct 11 2018
  Copyright (c) 2018 Hasan Gondal
*/

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dchest/uniuri"

	"github.com/PuerkitoBio/goquery"
	"github.com/orcaman/concurrent-map"

	"github.com/tidwall/gjson"

	"bitbucket.org/afraid/cloudflare"
	"bitbucket.org/afraid/riskified"
)

type taskOW struct {
	TaskNum     string
	TaskInfo    gjson.Result
	Client      *http.Client
	RXSessionID string
	RXCookieID  string
	UserAgent   string
	CSRFToken   string
	LocaleURL   string
	CurrentURL  string
	PreviousURL string
	CountryID   string
	GuestMode   bool
	VariantID   string
	LineItemID  string
	BillAddID   string
	ShipAddID   string
	StateLock   string
	ShipID      string
	ShipRateID  string
	OrderAmount string
	MerchantID  string
	OrderID     string

	PaymentFrameURL string
	RedirectURL     string
}

type variantOW struct {
	VariantID string
	Name      string
}

type productOW struct {
	Method   string
	Variants []*variantOW
}

type monitorOW struct {
	Client    *http.Client
	Proxy     *url.URL
	ProductID string
	UserAgent string
	Method    string
}

const (
	currentVersion    string = "alpha"
	shopURL           string = "www.off---white.com"
	baseURL           string = "https://www.off---white.com/"
	addToCartURL      string = "https://www.off---white.com/orders/populate.json"
	countryAPI        string = "https://www.off---white.com/api/countries.json"
	riskifiedAPI      string = "http://localhost:3000"
	paymentFrameURL   string = "https://ecomm.sella.it/Pagam/hiddenIframe.aspx"
	processTokenURL   string = "https://www.off---white.com/checkout/payment/process_token.json"
	noTaskPlaceholder string = "####"
	selectorHTML      string = `*[name="variant_id"]`
)

var (
	mu                sync.Mutex
	wg                sync.WaitGroup
	proxyArray        []*url.URL
	parsedBaseURL, _  = url.Parse(baseURL)
	userAgents        = []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:62.0) Gecko/20100101 Firefox/62.0", "Mozilla/5.0 (Windows NT 6.1; Win64; x64; rv:62.0) Gecko/20100101 Firefox/62.0", "Mozilla/5.0 (Windows NT 6.3; Win64; x64; rv:62.0) Gecko/20100101 Firefox/62.0", "Mozilla/5.0 (Windows NT 10.0; WOW64; rv:62.0) Gecko/20100101 Firefox/62.0", "Mozilla/5.0 (Windows NT 6.1; WOW64; rv:62.0) Gecko/20100101 Firefox/62.0", "Mozilla/5.0 (Windows NT 6.3; WOW64; rv:62.0) Gecko/20100101 Firefox/62.0", "Mozilla/5.0 (Windows NT 6.2; Win64; x64; rv:62.0) Gecko/20100101 Firefox/62.0"}
	proxyEnabled      = false
	monitoredProducts = cmap.New()
	instockProducts   = cmap.New()
	proxiesTask       = cmap.New()
	proxiesMonitor    = cmap.New()
)

func main() {
	rand.Seed(time.Now().Unix())
	cmdLog(noTaskPlaceholder, "info", "launching")
	cmdLog(noTaskPlaceholder, "version", currentVersion)
	if riskified.CheckAPIStatus(riskifiedAPI) == false {
		cmdLog(noTaskPlaceholder, "error", "api offline")
		cmdLog(noTaskPlaceholder, "error", "exiting")
		os.Exit(1)
	}
	cmdLog(noTaskPlaceholder, "success", "api online")
	proxyLoader()
}

func cmdLog(currentTask, currentStatus, messageOutput string) {
	fmt.Println(`[` + time.Now().Format("15:04:05.000") + `] [FLUID] [POISON] [` + strings.ToUpper(currentTask) + `] [` + strings.ToUpper(currentStatus) + `] [` + strings.ToUpper(messageOutput) + `]`)
}

func proxyLoader() {
	var unfilteredArray []string
	var proxyFile, fileErr = ioutil.ReadFile("./proxies.txt")
	if fileErr != nil {
		cmdLog(noTaskPlaceholder, "error", fileErr.Error())
	}
	unfilteredArray = strings.Split(string(proxyFile), ",")
	for _, individualProxy := range unfilteredArray {
		if individualProxy != "" {
			var proxyObject = strings.Split(individualProxy, ":")
			var formattedProxy *url.URL
			var parseError error
			if len(proxyObject) == 2 {
				formattedProxy, parseError = url.Parse(fmt.Sprintf("http://%v:%v", proxyObject[0], proxyObject[1]))
			} else if len(proxyObject) == 4 {
				formattedProxy, parseError = url.Parse(fmt.Sprintf("http://%v:%v@%v:%v", proxyObject[2], proxyObject[3], proxyObject[0], proxyObject[1]))
			} else {
				continue
			}

			if parseError != nil {
				cmdLog(noTaskPlaceholder, "error", parseError.Error())
				cmdLog(noTaskPlaceholder, "skipping proxy", fmt.Sprintf("%v", individualProxy))
				continue
			}
			proxyArray = append(proxyArray, formattedProxy)
		}
	}
	if len(proxyArray) == 0 {
		cmdLog(noTaskPlaceholder, "info", "running proxyless mode")
	} else {
		proxyEnabled = true
		cmdLog(noTaskPlaceholder, "success", fmt.Sprintf("proxies loaded - %v", len(proxyArray)))
	}
	taskLoader()
}

func proxyTest(proxyURL *url.URL) bool {
	var proxyClient = &http.Client{Timeout: 10 * time.Second}
	proxyClient.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL), IdleConnTimeout: 30 * time.Second}
	var proxyResp, proxyErr = proxyClient.Get(baseURL)
	if proxyErr != nil {
		cmdLog(noTaskPlaceholder, "error", proxyErr.Error())
		cmdLog(noTaskPlaceholder, "skipping proxy", proxyURL.String())
		return false
	}
	defer proxyResp.Body.Close()
	var _, ioErr = io.Copy(ioutil.Discard, proxyResp.Body)
	if ioErr != nil {
		return false
	}
	if proxyResp.StatusCode != 403 {
		return true
	}
	cmdLog(noTaskPlaceholder, "error", fmt.Sprintf("%v returned", proxyResp.StatusCode))
	cmdLog(noTaskPlaceholder, "skipping proxy", proxyURL.String())
	return false
}

func taskLoader() {
	var tasksByteArray, tasksErr = ioutil.ReadFile("./tasks.json")
	if tasksErr != nil {
		cmdLog(noTaskPlaceholder, "error", "could not load tasks")
		cmdLog(noTaskPlaceholder, "error", "exiting")
		os.Exit(1)
	} else if tasksByteArray == nil {
		cmdLog(noTaskPlaceholder, "error", "tasks are empty")
		cmdLog(noTaskPlaceholder, "error", "exiting")
		os.Exit(1)
	}
	var parsedTaskArray = gjson.ParseBytes(tasksByteArray)
	var taskAmount = parsedTaskArray.Get("#").Int()
	if taskAmount == 0 {
		cmdLog(noTaskPlaceholder, "error", "there are no tasks")
		cmdLog(noTaskPlaceholder, "error", "exiting")
		os.Exit(1)
	}
	cmdLog(noTaskPlaceholder, "success", fmt.Sprintf("tasks loaded - %v", taskAmount))

	var taskPIDArray []string
	var productsUnfiltered = parsedTaskArray.Get("#.productInformation.productIdentifier").Array()
	for _, currentPID := range productsUnfiltered {
		var inArrayAlready = false
		for _, individualPID := range taskPIDArray {
			if currentPID.String() == individualPID {
				inArrayAlready = true
			}
		}
		if inArrayAlready == false {
			taskPIDArray = append(taskPIDArray, currentPID.String())
		}
	}
	cmdLog(noTaskPlaceholder, "success", fmt.Sprintf("products to monitor loaded - %v", len(taskPIDArray)))
	threadHandler(parsedTaskArray, taskPIDArray)
}

func threadHandler(taskArray gjson.Result, taskPIDArray []string) {
	for _, individualPID := range taskPIDArray {
		wg.Add(1)
		go func(individualPID string) {
			cmdLog("mntr", "info", fmt.Sprintf("now monitoring - %v", individualPID))
			monitoredProducts.Set(individualPID, false)
			taskMonitorHandler(individualPID)
			defer wg.Done()
		}(individualPID)
	}
	for taskIndex, individualTask := range taskArray.Array() {
		wg.Add(1)
		go func(taskIndex int, taskInformation gjson.Result) {
			var taskNumber = fmt.Sprintf("000%v", taskIndex)
			taskNumber = taskNumber[len(taskNumber)-4:]
			taskPrequisites(taskNumber, taskInformation)
			defer wg.Done()
		}(taskIndex, individualTask)
	}
	wg.Wait()
}

func getRandomUA() string {
	return userAgents[rand.Intn(len(userAgents))]
}

func setRandomProxy(taskNumber string, taskClient *http.Client, retryAttempts int) {
	if retryAttempts == 0 {
		cmdLog(taskNumber, "error", "exiting thread maximum proxies retries hit")
		wg.Done()
	}
	var randomProxy = proxyArray[rand.Intn(len(proxyArray))]
	if proxiesTask.SetIfAbsent(randomProxy.String(), true) && proxyTest(randomProxy) {
		taskClient.Transport = &http.Transport{Proxy: http.ProxyURL(randomProxy)}
		cmdLog(taskNumber, "success", fmt.Sprintf("set proxy - %v", randomProxy.String()))
	} else {
		cmdLog(taskNumber, "error", fmt.Sprintf("proxy in use, retrying - %v", randomProxy.String()))
		retryAttempts--
		setRandomProxy(taskNumber, taskClient, retryAttempts)
	}
}

func setLastRxRun(taskClient *http.Client) {
	var lastRxRunCookie = &http.Cookie{Name: "lastRskxRun", Value: fmt.Sprintf("%v", int(time.Now().UnixNano()/1e6))}
	var createdCookies = []*http.Cookie{lastRxRunCookie}
	taskClient.Jar.SetCookies(parsedBaseURL, createdCookies)
}

func taskPrequisites(taskNumber string, taskInformation gjson.Result) {
	var userAgent = getRandomUA()
	cmdLog(taskNumber, "info", fmt.Sprintf("starting task - %v", taskInformation.Get("accountInformation.emailAddress").String()))
	var taskClient = &http.Client{Timeout: 20 * time.Second}
	if proxyEnabled {
		var retryAttempts = 5
		setRandomProxy(taskNumber, taskClient, retryAttempts)
	}
	var taskJar, _ = cookiejar.New(nil)
	taskClient.Jar = taskJar
	var rxSessionCookieSet = riskified.GetSessionCookieSet(riskifiedAPI)
	var rxSession = &http.Cookie{Name: "__riskifiedBeaconSessionId", Value: rxSessionCookieSet[0]}
	var rxCookie = &http.Cookie{Name: "rCookie", Value: rxSessionCookieSet[1]}
	var rxRan = &http.Cookie{Name: "rskxRunCookie", Value: "0"}
	var dismissCookie = &http.Cookie{Name: "dismiss_cookie_law", Value: "true"}
	var createdCookies = []*http.Cookie{rxCookie, rxSession, rxRan, dismissCookie}
	taskClient.Jar.SetCookies(parsedBaseURL, createdCookies)
	cmdLog(taskNumber, "success", "set riskified cookies")
	var taskObject = &taskOW{TaskNum: taskNumber, TaskInfo: taskInformation, Client: taskClient, RXSessionID: rxSessionCookieSet[0], RXCookieID: rxSessionCookieSet[1], UserAgent: userAgent}
	taskVisitHomepage(taskObject)
}

func setCFCookies(taskObject *taskOW) {
	var cookiesCF = cloudflare.GetTokens(taskObject.CurrentURL, taskObject.UserAgent, taskObject.Client)
	if len(cookiesCF) == 2 {
		taskObject.Client.Jar.SetCookies(parsedBaseURL, cookiesCF)
	} else {
		setCFCookies(taskObject)
	}
}

func taskCFHandler(taskObject *taskOW) {
	var statusCF = cloudflare.IsRestricted(taskObject.CurrentURL, taskObject.UserAgent, taskObject.Client)
	if statusCF == "JAVASCRIPT_CHALLENGE" || statusCF == "RECAPTCHA_CHALLENGE" {
		cmdLog(taskObject.TaskNum, "info", "detected cloudflare challenge")
		setCFCookies(taskObject)
		cmdLog(taskObject.TaskNum, "success", "set cloudflare cookies")
	} else if statusCF == "BANNED_IP" {
		cmdLog(taskObject.TaskNum, "error", "cloudflare ip banned")
		cmdLog(taskObject.TaskNum, "info", "setting new proxy")
		if proxyEnabled {
			var retryAttempts = 5
			setRandomProxy(taskObject.TaskNum, taskObject.Client, retryAttempts)
			taskCFHandler(taskObject)
		} else {
			cmdLog(taskObject.TaskNum, "error", "cloudflare local ip banned")
			cmdLog(taskObject.TaskNum, "info", "exiting thread")
			wg.Done()
		}
	} else if statusCF == "NO_CHALLENGE" {
		cmdLog(taskObject.TaskNum, "info", "cloudflare passed")
	} else {
		taskCFBackground(taskObject)
	}
}

func taskCFBackground(taskObject *taskOW) {
	var statusCF = cloudflare.IsRestricted(baseURL, taskObject.UserAgent, taskObject.Client)
	if statusCF == "JAVASCRIPT_CHALLENGE" || statusCF == "RECAPTCHA_CHALLENGE" {
		cmdLog(taskObject.TaskNum, "info", "detected cloudflare challenge")
		setCFCookies(taskObject)
		cmdLog(taskObject.TaskNum, "success", "set cloudflare cookies")
		time.Sleep(5 * time.Minute)
		taskCFBackground(taskObject)
	} else if statusCF == "BANNED_IP" {
		cmdLog(taskObject.TaskNum, "error", "cloudflare ip banned")
		cmdLog(taskObject.TaskNum, "info", "setting new proxy")
		if proxyEnabled {
			var retryAttempts = 10
			setRandomProxy(taskObject.TaskNum, taskObject.Client, retryAttempts)
			taskCFBackground(taskObject)
		} else {
			cmdLog(taskObject.TaskNum, "error", "cloudflare local ip banned")
			cmdLog(taskObject.TaskNum, "info", "exiting thread")
			wg.Done()
		}
	} else if statusCF == "NO_CHALLENGE" {
		cmdLog(taskObject.TaskNum, "info", "cloudflare clearance live")
		time.Sleep(5 * time.Minute)
		taskCFBackground(taskObject)
	} else {
		taskCFBackground(taskObject)
	}
}

func taskVisitHomepage(taskObject *taskOW) {
	taskObject.CurrentURL = baseURL
	taskCFHandler(taskObject)

	go taskCFBackground(taskObject)

	var homeReq, _ = http.NewRequest("GET", taskObject.CurrentURL, nil)
	homeReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	homeReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	homeReq.Header.Set("Referer", taskObject.CurrentURL)
	homeReq.Header.Set("DNT", "1")
	homeReq.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(homeReq)

	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "error", "retrying homepage request")
		taskVisitHomepage(taskObject)
	} else {
		defer reqResp.Body.Close()
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskVisitHomepage(taskObject)
		} else {
			taskObject.PreviousURL = baseURL
			taskObject.LocaleURL, taskObject.CurrentURL = reqResp.Request.URL.String(), reqResp.Request.URL.String()
			riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
			setLastRxRun(taskObject.Client)
			cmdLog(taskObject.TaskNum, "success", "visited off---white homepage")
			taskAccountHandler(taskObject)
		}
	}
}

func taskVisitLoginPage(taskObject *taskOW) {
	var loginURL = taskObject.CurrentURL + "/login"
	var loginPageReq, _ = http.NewRequest("GET", loginURL, nil)
	loginPageReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	loginPageReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	loginPageReq.Header.Set("Referer", taskObject.CurrentURL)
	loginPageReq.Header.Set("DNT", "1")
	loginPageReq.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(loginPageReq)

	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "login page request")
		taskVisitLoginPage(taskObject)
	} else {
		defer reqResp.Body.Close()
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskVisitLoginPage(taskObject)
		} else {
			taskObject.PreviousURL = taskObject.CurrentURL
			taskObject.CurrentURL = reqResp.Request.URL.String()
			riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
			setLastRxRun(taskObject.Client)
			cmdLog(taskObject.TaskNum, "success", "visited off---white login page")
			taskLogin(taskObject)
		}
	}
}

func taskAccountHandler(taskObject *taskOW) {
	if taskObject.TaskInfo.Get("accountInformation.guestCheckout").Bool() {
		cmdLog(taskObject.TaskNum, "info", "using guest checkout")
		taskSetCountryID(taskObject)
		taskObject.GuestMode = true
		taskObject.CSRFToken = ""
		taskVisitPDP(taskObject)
	} else {
		cmdLog(taskObject.TaskNum, "info", fmt.Sprintf("attempting to login to pre-exising account"))
		taskObject.GuestMode = false
		taskVisitLoginPage(taskObject)
	}
}

func taskLogin(taskObject *taskOW) {
	var loginForm = url.Values{}
	loginForm.Add("utf8", "✓")
	loginForm.Add("authenticity_token", "")
	loginForm.Add("spree_user[email]", taskObject.TaskInfo.Get("accountInformation.emailAddress").String())
	loginForm.Add("spree_user[password]", taskObject.TaskInfo.Get("accountInformation.accountPassword").String())
	loginForm.Add("spree_user[remember_me]", "0")
	loginForm.Add("spree_user[remember_me]", "1")
	loginForm.Add("commit", "Login")

	var loginReq, _ = http.NewRequest("POST", taskObject.CurrentURL, strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	loginReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	loginReq.Header.Set("Referer", taskObject.CurrentURL)
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("DNT", "1")
	loginReq.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(loginReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "login request")
		taskLogin(taskObject)
	} else {
		var respPageHTML, _ = goquery.NewDocumentFromResponse(reqResp)
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskLogin(taskObject)
		} else {
			defer reqResp.Body.Close()
			taskObject.PreviousURL = taskObject.CurrentURL
			taskObject.CurrentURL = reqResp.Request.URL.String()
			riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
			setLastRxRun(taskObject.Client)
			if taskObject.PreviousURL != taskObject.CurrentURL {
				var tokenCSRF, _ = respPageHTML.Find(`*[name="csrf-token"]`).Attr("content")
				if len(tokenCSRF) != 0 {
					taskObject.CSRFToken = tokenCSRF
					cmdLog(taskObject.TaskNum, "success", fmt.Sprintf("logged in, got csrf token - %v", tokenCSRF))
					taskSetCountryID(taskObject)
					taskVisitPDP(taskObject)
				} else {
					cmdLog(taskObject.TaskNum, "error", "could not get csrf token, exiting thread")
					wg.Done()
				}
			} else {
				cmdLog(taskObject.TaskNum, "error", "invalid credentials, exiting thread")
				wg.Done()
			}
		}
	}
}

func taskSetCountryID(taskObject *taskOW) {
	var apiReq, _ = http.NewRequest("GET", countryAPI, nil)
	apiReq.Header.Set("Accept", "application/json")
	apiReq.Header.Set("Referer", baseURL)
	apiReq.Header.Set("User-Agent", taskObject.UserAgent)
	var countryQuery = apiReq.URL.Query()
	countryQuery.Add("q[name_cont]", taskObject.TaskInfo.Get("checkoutInformation.addressInformation.addressCountry").String())
	apiReq.URL.RawQuery = countryQuery.Encode()

	var reqResp, reqErr = taskObject.Client.Do(apiReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "country api request")
		taskSetCountryID(taskObject)
	} else {
		defer reqResp.Body.Close()
		var respByteArray, _ = ioutil.ReadAll(reqResp.Body)
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskSetCountryID(taskObject)
		} else {
			var countryJSON = gjson.ParseBytes(respByteArray)
			if countryJSON.Get("count").Int() == 1 && countryJSON.Get("countries.0.id").String() != "" {
				taskObject.CountryID = countryJSON.Get("countries.0.id").String()
				cmdLog(taskObject.TaskNum, "success", fmt.Sprintf("set country id - %v", taskObject.CountryID))
			} else {
				cmdLog(taskObject.TaskNum, "error", "invalid country, exiting thread")
			}
		}
	}
}

func taskVisitPDP(taskObject *taskOW) {
	var productURL = taskObject.LocaleURL + "/products/" + taskObject.TaskInfo.Get("productInformation.productIdentifier").String()
	var pdpReq, _ = http.NewRequest("GET", productURL, nil)
	pdpReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	pdpReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	pdpReq.Header.Set("Referer", productURL)
	pdpReq.Header.Set("DNT", "1")
	pdpReq.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(pdpReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "pdp request")
		taskVisitPDP(taskObject)
	} else {
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskObject.PreviousURL = baseURL
			taskVisitPDP(taskObject)
		} else {
			defer reqResp.Body.Close()
			taskObject.PreviousURL = productURL
			taskObject.CurrentURL = reqResp.Request.URL.String()
			riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
			setLastRxRun(taskObject.Client)
			cmdLog(taskObject.TaskNum, "success", fmt.Sprintf("visited pdp - %v", taskObject.TaskInfo.Get("productInformation.productIdentifier").String()))
			cmdLog(taskObject.TaskNum, "info", fmt.Sprintf("awaiting product - %v", taskObject.TaskInfo.Get("productInformation.productIdentifier").String()))
			taskAwaitProduct(taskObject)
		}
	}
}

func taskMonitorHandler(productIdentifier string) {
	rand.Seed(time.Now().Unix())
	for mntrIndex := 0; mntrIndex < 24; mntrIndex++ {
		wg.Add(1)
		var monitorMethod = "JSON"
		if mntrIndex%4 == 0 {
			monitorMethod = "HTML"
		}
		go func(productIdentifier string, mntrIndex int) {
			cmdLog("mntr", "info", fmt.Sprintf("creating thread [%v] [%v] - %v", mntrIndex, monitorMethod, productIdentifier))
			monitorThread(productIdentifier, monitorMethod)
		}(productIdentifier, mntrIndex)
	}
}

func monitorThread(productIdentifier string, monitorMethod string) {
	var monitorClient = &http.Client{Timeout: 20 * time.Second}
	monitorClient.Transport = &http.Transport{IdleConnTimeout: 90 * time.Second, MaxIdleConnsPerHost: 2}
	var monitorObject = &monitorOW{Client: monitorClient, ProductID: productIdentifier, Method: monitorMethod}
	var monitorJar, _ = cookiejar.New(nil)
	monitorObject.Client.Jar = monitorJar
	monitorObject.UserAgent = getRandomUA()
	if proxyEnabled {
		setMonitorProxy(monitorObject)
	}
	monitorCFHandler(monitorObject)
	monitorHub(monitorObject)
}

func monitorHub(monitorObject *monitorOW) {
	switch monitorObject.Method {
	case "JSON":
		monitorJSON(monitorObject)
	case "HTML":
		monitorHTML(monitorObject)
	}
}

func monitorJSON(monitorObject *monitorOW) {
	var productStockStatus, _ = monitoredProducts.Get(monitorObject.ProductID)
	if productStockStatus == false {
		var productURL = baseURL + "products/" + monitorObject.ProductID + ".json?" + uniuri.NewLen(32) + "=" + uniuri.NewLen(32)
		var reqJSON, _ = http.NewRequest("GET", productURL, nil)
		reqJSON.Header.Set("User-Agent", monitorObject.UserAgent)

		var reqResp, reqErr = monitorObject.Client.Do(reqJSON)
		if reqErr != nil {
			time.Sleep(200 * time.Millisecond)
			monitorJSON(monitorObject)
		} else {
			defer reqResp.Body.Close()
			var respByteArray, _ = ioutil.ReadAll(reqResp.Body)
			if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
				monitorCFHandler(monitorObject)
				monitorJSON(monitorObject)
			} else if reqResp.StatusCode != 404 {
				var productJSON = gjson.ParseBytes(respByteArray)
				if productJSON.Get("available_sizes.#").Int() > 0 {
					productStockStatus, _ = monitoredProducts.Get(monitorObject.ProductID)
					if productStockStatus == false {
						var individualVariant gjson.Result
						var variantArray []*variantOW
						for _, individualVariant = range productJSON.Get("available_sizes").Array() {
							variantArray = append(variantArray, &variantOW{VariantID: individualVariant.Get("id").String(), Name: individualVariant.Get("name").String()})
						}
						var productObject = &productOW{Method: "JSON", Variants: variantArray}
						monitoredProducts.Set(monitorObject.ProductID, true)
						instockProducts.Set(monitorObject.ProductID, productObject)
						cmdLog("mntr", "info", fmt.Sprintf("now instock [json] - %v", monitorObject.ProductID))
						if proxyEnabled {
							proxiesMonitor.Remove(monitorObject.Proxy.String())
							proxiesTask.Remove(monitorObject.Proxy.String())
						}
					} else {
						if proxyEnabled {
							proxiesMonitor.Remove(monitorObject.Proxy.String())
							proxiesTask.Remove(monitorObject.Proxy.String())
						}

					}
				} else {
					cmdLog("mntr", "info", fmt.Sprintf("oos - %v", monitorObject.ProductID))
					if proxyEnabled {
						proxiesMonitor.Remove(monitorObject.Proxy.String())
						proxiesTask.Remove(monitorObject.Proxy.String())
					}
					var monitorJar, _ = cookiejar.New(nil)
					monitorObject.Client.Jar = monitorJar
					monitorObject.UserAgent = getRandomUA()
					setMonitorProxy(monitorObject)
					monitorCFHandler(monitorObject)
					monitorJSON(monitorObject)
				}
			} else {
				cmdLog("mntr", "info", fmt.Sprintf("oos - %v", monitorObject.ProductID))
				if proxyEnabled {
					proxiesMonitor.Remove(monitorObject.Proxy.String())
					proxiesTask.Remove(monitorObject.Proxy.String())
				}
				var monitorJar, _ = cookiejar.New(nil)
				monitorObject.Client.Jar = monitorJar
				monitorObject.UserAgent = getRandomUA()
				setMonitorProxy(monitorObject)
				monitorCFHandler(monitorObject)
				monitorJSON(monitorObject)
			}
		}
	} else {
		if proxyEnabled {
			proxiesMonitor.Remove(monitorObject.Proxy.String())
			proxiesTask.Remove(monitorObject.Proxy.String())
		}
		wg.Done()
	}
}

func monitorHTML(monitorObject *monitorOW) {
	var productStockStatus, _ = monitoredProducts.Get(monitorObject.ProductID)
	if productStockStatus == false {
		var productStockStatus, _ = monitoredProducts.Get(monitorObject.ProductID)
		if productStockStatus == true {
			if proxyEnabled {
				proxiesMonitor.Remove(monitorObject.Proxy.String())
				proxiesTask.Remove(monitorObject.Proxy.String())
			}
		} else {
			var productURL = baseURL + "products/" + monitorObject.ProductID + "." + uniuri.NewLen(64)
			var reqHTML, _ = http.NewRequest("GET", productURL, nil)
			reqHTML.Header.Set("User-Agent", monitorObject.UserAgent)

			var reqResp, reqErr = monitorObject.Client.Do(reqHTML)
			if reqErr != nil || reqResp == nil {
				time.Sleep(200 * time.Millisecond)
				monitorHTML(monitorObject)
			} else {
				var productHTML, gqLoadErr = goquery.NewDocumentFromResponse(reqResp)
				if gqLoadErr != nil {
					time.Sleep(200 * time.Millisecond)
					monitorHTML(monitorObject)
				} else {
					if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
						monitorCFHandler(monitorObject)
						monitorHTML(monitorObject)
					} else if reqResp.StatusCode != 404 {
						var variantArray []*variantOW
						productHTML.Find(selectorHTML).Each(func(i int, individualSelection *goquery.Selection) {
							var variantID, itemExists = individualSelection.Attr("value")
							var sizeSelector, _ = individualSelection.Attr("id")
							if itemExists {
								var itemSize = productHTML.Find(fmt.Sprintf(`*[for="%v"]`, sizeSelector)).Text()
								variantArray = append(variantArray, &variantOW{VariantID: variantID, Name: itemSize})
							}
						})
						if len(variantArray) > 0 {
							var productStockStatus, _ = monitoredProducts.Get(monitorObject.ProductID)
							if productStockStatus == false {
								var productObject = &productOW{Method: "HTML", Variants: variantArray}
								monitoredProducts.Set(monitorObject.ProductID, true)
								instockProducts.Set(monitorObject.ProductID, productObject)
								cmdLog("mntr", "info", fmt.Sprintf("now instock [html] - %v", monitorObject.ProductID))
								if proxyEnabled {
									proxiesMonitor.Remove(monitorObject.Proxy.String())
									proxiesTask.Remove(monitorObject.Proxy.String())
								}

							} else {
								if proxyEnabled {
									proxiesMonitor.Remove(monitorObject.Proxy.String())
									proxiesTask.Remove(monitorObject.Proxy.String())
								}
							}
						} else {
							cmdLog("mntr", "info", fmt.Sprintf("oos - %v", monitorObject.ProductID))
							if proxyEnabled {
								proxiesMonitor.Remove(monitorObject.Proxy.String())
								proxiesTask.Remove(monitorObject.Proxy.String())
							}
							var monitorJar, _ = cookiejar.New(nil)
							monitorObject.Client.Jar = monitorJar
							monitorObject.UserAgent = getRandomUA()
							setMonitorProxy(monitorObject)
							monitorCFHandler(monitorObject)
							monitorHTML(monitorObject)
						}
					} else {
						cmdLog("mntr", "info", fmt.Sprintf("oos - %v", monitorObject.ProductID))
						if proxyEnabled {
							proxiesMonitor.Remove(monitorObject.Proxy.String())
							proxiesTask.Remove(monitorObject.Proxy.String())
						}
						var monitorJar, _ = cookiejar.New(nil)
						monitorObject.Client.Jar = monitorJar
						monitorObject.UserAgent = getRandomUA()
						setMonitorProxy(monitorObject)
						monitorCFHandler(monitorObject)
						monitorHTML(monitorObject)
					}
				}
			}
		}
	} else {
		if proxyEnabled {
			proxiesMonitor.Remove(monitorObject.Proxy.String())
			proxiesTask.Remove(monitorObject.Proxy.String())
		}
		wg.Done()
	}
}

func setMonitorProxy(monitorObject *monitorOW) {
	var mntrProxy = proxyArray[rand.Intn(len(proxyArray))]
	if proxiesMonitor.SetIfAbsent(mntrProxy.String(), true) && proxiesTask.SetIfAbsent(mntrProxy.String(), true) && proxyTest(mntrProxy) {
		proxiesMonitor.Set(mntrProxy.String(), true)
		monitorObject.Client.Transport = &http.Transport{Proxy: http.ProxyURL(mntrProxy)}
		monitorObject.Proxy = mntrProxy
	} else {
		setMonitorProxy(monitorObject)
	}
}

func setCFCookiesMonitor(monitorObject *monitorOW) {
	var cookiesCF = cloudflare.GetTokens(baseURL, monitorObject.UserAgent, monitorObject.Client)
	if len(cookiesCF) == 2 {
		monitorObject.Client.Jar.SetCookies(parsedBaseURL, cookiesCF)
	} else {
		setCFCookiesMonitor(monitorObject)
	}
}

func monitorCFHandler(monitorObject *monitorOW) {
	var productStockStatus, _ = monitoredProducts.Get(monitorObject.ProductID)
	if productStockStatus == false {
		var statusCF = cloudflare.IsRestricted(baseURL, monitorObject.UserAgent, monitorObject.Client)
		if statusCF == "JAVASCRIPT_CHALLENGE" || statusCF == "RECAPTCHA_CHALLENGE" {
			setCFCookiesMonitor(monitorObject)
		} else if statusCF == "BANNED_IP" {
			fmt.Println(monitorObject.Proxy.String())
			cmdLog("mntr", "error", "cloudflare ip banned")
			cmdLog("mntr", "info", "setting new proxy")
			if proxyEnabled {
				setMonitorProxy(monitorObject)
				monitorCFHandler(monitorObject)
			} else {
				cmdLog("mntr", "error", "cloudflare local ip banned")
				cmdLog("mntr", "info", "exiting thread")
				wg.Done()
			}
		} else if statusCF == "NO_CHALLENGE" {
		}
	}
}

func taskAwaitProduct(taskObject *taskOW) {
	var variantsAvailable, _ = monitoredProducts.Get(taskObject.TaskInfo.Get("productInformation.productIdentifier").String())
	if variantsAvailable == true {
		cmdLog(taskObject.TaskNum, "info", fmt.Sprintf("attempting to cart - %v", taskObject.TaskInfo.Get("productInformation.productIdentifier").String()))
		taskCartingHandler(taskObject)
	} else {
		time.Sleep(50 * time.Millisecond)
		taskAwaitProduct(taskObject)
	}
}

func taskCartingHandler(taskObject *taskOW) {
	var sizeMethod string
	switch sizeMethod = taskObject.TaskInfo.Get("productInformation.variantMethod").String(); sizeMethod {
	case "Random":
		taskCartRandom(taskObject)
	}
}

func taskCartRandom(taskObject *taskOW) {
	cmdLog(taskObject.TaskNum, "info", fmt.Sprintf("randomly choosing variant"))
	var productObject, _ = instockProducts.Get(taskObject.TaskInfo.Get("productInformation.productIdentifier").String())
	var variantArray = productObject.(*productOW).Variants
	var randomVariant = variantArray[rand.Intn(len(variantArray))]
	taskObject.VariantID = randomVariant.VariantID
	cmdLog(taskObject.TaskNum, "info", fmt.Sprintf("chosen variant [%v - %v]", randomVariant.VariantID, randomVariant.Name))
	taskCartRequest(taskObject)
}

func taskCartRequest(taskObject *taskOW) {
	var cartPayload = []byte(fmt.Sprintf(`{"variant_id":%v, "quantity":1}`, taskObject.VariantID))
	var cartReq, _ = http.NewRequest("POST", fmt.Sprintf("%v?%v", addToCartURL, uniuri.NewLen(16)), bytes.NewBuffer(cartPayload))
	cartReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	cartReq.Header.Set("Referer", taskObject.CurrentURL)
	cartReq.Header.Set("Content-Type", "application/json")
	cartReq.Header.Set("DNT", "1")
	cartReq.Header.Set("User-Agent", taskObject.UserAgent)
	cartReq.Header.Set("X-Requested-With", "XMLHttpRequest")

	var reqResp, reqErr = taskObject.Client.Do(cartReq)

	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "carting request")
		taskCartRequest(taskObject)
	} else {
		defer reqResp.Body.Close()
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskCartRequest(taskObject)
		} else {
			if reqResp.StatusCode == 200 {
				cmdLog(taskObject.TaskNum, "success", fmt.Sprintf("carted variant - %v", taskObject.VariantID))
				taskVisitCart(taskObject)
			} else {
				cmdLog(taskObject.TaskNum, "error", fmt.Sprintf("could not cart variant, retrying - %v", taskObject.VariantID))
				time.Sleep(500 * time.Millisecond)
				taskCartRandom(taskObject)
			}
		}
	}
}

func taskVisitCart(taskObject *taskOW) {
	var cartURL = taskObject.LocaleURL + "/cart"
	var cartPageReq, _ = http.NewRequest("GET", cartURL, nil)
	cartPageReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	cartPageReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	cartPageReq.Header.Set("Referer", taskObject.CurrentURL)
	cartPageReq.Header.Set("DNT", "1")
	cartPageReq.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(cartPageReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "visit cart request")
		taskVisitCart(taskObject)
	} else {
		var loadedHTML, _ = goquery.NewDocumentFromResponse(reqResp)
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskObject.PreviousURL = baseURL
			taskVisitPDP(taskObject)
		} else {
			var lineItemID, _ = loadedHTML.Find("#order_line_items_attributes_0_id").Attr("value")
			if len(lineItemID) > 0 {
				taskObject.LineItemID = lineItemID
				taskObject.PreviousURL = taskObject.CurrentURL
				taskObject.CurrentURL = reqResp.Request.URL.String()
				riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
				setLastRxRun(taskObject.Client)
				cmdLog(taskObject.TaskNum, "success", fmt.Sprintf("visited cart page, got line item id - %v", lineItemID))
				taskCheckoutHandler(taskObject)
			} else {
				cmdLog(taskObject.TaskNum, "error", "could not visit cart page, retrying")
				time.Sleep(200 * time.Millisecond)
				taskVisitCart(taskObject)
			}
		}
	}
}

func taskCheckoutHandler(taskObject *taskOW) {
	if taskObject.GuestMode {
		taskPostGuestCart(taskObject)
	} else {
		//taskPostAccountCart(taskObject)
	}
}

func taskPostGuestCart(taskObject *taskOW) {
	var checkoutForm = url.Values{}
	checkoutForm.Add("utf8", "✓")
	checkoutForm.Add("_method", "patch")
	checkoutForm.Add("authenticity_token", taskObject.CSRFToken)
	checkoutForm.Add("order[line_items_attributes][0][quantity]", "1")
	checkoutForm.Add("order[line_items_attributes][0][id]", taskObject.LineItemID)
	checkoutForm.Add("order[coupon_code]", "")
	checkoutForm.Add("checkout", "")
	var guestCartReq, _ = http.NewRequest("POST", taskObject.CurrentURL, strings.NewReader(checkoutForm.Encode()))
	guestCartReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	guestCartReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	guestCartReq.Header.Set("Referer", taskObject.CurrentURL)
	guestCartReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	guestCartReq.Header.Set("DNT", "1")
	guestCartReq.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(guestCartReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "setting guest checkout request")
		taskPostGuestCart(taskObject)
	} else {
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskPostGuestCart(taskObject)
		} else {
			defer reqResp.Body.Close()
			if reqResp.StatusCode == 200 {
				taskObject.PreviousURL = taskObject.CurrentURL
				taskObject.CurrentURL = reqResp.Request.URL.String()
				riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
				setLastRxRun(taskObject.Client)
				cmdLog(taskObject.TaskNum, "success", "posted guest cart")
				taskSetGuestCheckout(taskObject)

			} else {
				cmdLog(taskObject.TaskNum, "error", "could not post guest cart, retrying")
				time.Sleep(200 * time.Millisecond)
				taskPostGuestCart(taskObject)
			}
		}
	}
}

// func taskPostAccountCart(taskObject *taskOW) {
// 	var checkoutForm = url.Values{}
// 	checkoutForm.Add("utf8", "✓")
// 	checkoutForm.Add("_method", "patch")
// 	checkoutForm.Add("authenticity_token", taskObject.CSRFToken)
// 	checkoutForm.Add("order[line_items_attributes][0][quantity]", "1")
// 	checkoutForm.Add("order[line_items_attributes][0][id]", taskObject.LineItemID)
// 	checkoutForm.Add("order[coupon_code]", "")
// 	checkoutForm.Add("checkout", "")

// 	var accountCartReq, _ = http.NewRequest("POST", taskObject.CurrentURL, strings.NewReader(checkoutForm.Encode()))
// 	accountCartReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
// 	accountCartReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
// 	accountCartReq.Header.Set("Referer", taskObject.CurrentURL)
// 	accountCartReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
// 	accountCartReq.Header.Set("DNT", "1")
// 	accountCartReq.Header.Set("User-Agent", taskObject.UserAgent)

// 	var reqResp, reqErr = taskObject.Client.Do(accountCartReq)

// 	if reqErr != nil {
// 		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
// 		cmdLog(taskObject.TaskNum, "retrying", "setting guest checkout request")
// 		taskPostGuestCart(taskObject)
// 	} else {
// 		var loadedHTML, _ = goquery.NewDocumentFromResponse(reqResp)
// 		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
// 			taskCFHandler(taskObject)
// 			taskPostAccountCart(taskObject)
// 		} else {
// 			if reqResp.StatusCode == 200 {
// 				taskObject.PreviousURL = taskObject.CurrentURL
// 				taskObject.CurrentURL = reqResp.Request.URL.String()
// 				riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
// 				setLastRxRun(taskObject.Client)
// 				cmdLog(taskObject.TaskNum, "success", "posted account cart")
// 				//taskSetBilling(taskObject)
// 			} else {
// 				cmdLog(taskObject.TaskNum, "error", "could not post account cart, retrying")
// 				fmt.Print(loadedHTML.Text())
// 				// time.Sleep(200 * time.Millisecond)
// 				// taskPostAccountCart(taskObject)
// 			}
// 		}
// 	}
// }

func taskSetGuestCheckout(taskObject *taskOW) {
	var guestForm = url.Values{}
	guestForm.Add("utf8", "✓")
	guestForm.Add("_method", "put")
	guestForm.Add("authenticity_token", taskObject.CSRFToken)
	guestForm.Add("order[email]", taskObject.TaskInfo.Get("accountInformation.emailAddress").String())
	guestForm.Add("commit", "Continue")
	var guestReq, _ = http.NewRequest("POST", taskObject.CurrentURL, strings.NewReader(guestForm.Encode()))
	guestReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	guestReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	guestReq.Header.Set("Referer", taskObject.CurrentURL)
	guestReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	guestReq.Header.Set("DNT", "1")
	guestReq.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(guestReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "setting guest checkout request")
		taskSetGuestCheckout(taskObject)
	} else {
		var loadedHTML, _ = goquery.NewDocumentFromResponse(reqResp)
		var stateLock, _ = loadedHTML.Find("#order_state_lock_version").Attr("value")
		var billAddressID, _ = loadedHTML.Find("#order_bill_address_attributes_id").Attr("value")
		var shipAddressID, _ = loadedHTML.Find("#order_ship_address_attributes_id").Attr("value")
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskSetGuestCheckout(taskObject)
		} else {
			if reqResp.StatusCode == 200 && len(reqResp.Cookies()) == 2 && len(billAddressID) > 0 && len(shipAddressID) > 0 && len(stateLock) > 0 {
				taskObject.StateLock = stateLock
				taskObject.BillAddID = billAddressID
				taskObject.ShipAddID = shipAddressID
				taskObject.PreviousURL = taskObject.CurrentURL
				taskObject.CurrentURL = reqResp.Request.URL.String()
				riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
				setLastRxRun(taskObject.Client)
				cmdLog(taskObject.TaskNum, "success", "set guest checkout, got address attributes")
				taskSetBilling(taskObject)
			} else if reqResp.Request.URL.String() == taskObject.LocaleURL+"/cart" {
				cmdLog(taskObject.TaskNum, "error", "item oos")
				//HANDLE OOS
			} else {
				cmdLog(taskObject.TaskNum, "error", "could not set guest checkout, retrying")
				taskSetGuestCheckout(taskObject)
			}
		}
	}
}

func taskSetBilling(taskObject *taskOW) {
	var billingForm = url.Values{}
	billingForm.Add("utf8", "✓")
	billingForm.Add("_method", "patch")
	billingForm.Add("authenticity_token", taskObject.CSRFToken)
	billingForm.Add("order[email]", taskObject.TaskInfo.Get("accountInformation.emailAddress").String())
	billingForm.Add("order[state_lock_version]", taskObject.StateLock)
	billingForm.Add("order[bill_address_attributes][firstname]", taskObject.TaskInfo.Get("checkoutInformation.firstName").String())
	billingForm.Add("order[bill_address_attributes][lastname]", taskObject.TaskInfo.Get("checkoutInformation.lastName").String())
	billingForm.Add("order[bill_address_attributes][address1]", taskObject.TaskInfo.Get("checkoutInformation.addressInformation.addressLineOne").String())
	billingForm.Add("order[bill_address_attributes][address2]", taskObject.TaskInfo.Get("checkoutInformation.addressInformation.addressLineTwo").String())
	billingForm.Add("order[bill_address_attributes][city]", taskObject.TaskInfo.Get("checkoutInformation.addressInformation.addressCity").String())
	billingForm.Add("order[bill_address_attributes][country_id]", taskObject.CountryID)
	billingForm.Add("order[bill_address_attributes][zipcode]", taskObject.TaskInfo.Get("checkoutInformation.addressInformation.addressPostcode").String())
	billingForm.Add("order[bill_address_attributes][phone]", taskObject.TaskInfo.Get("accountInformation.phoneNumber").String())
	billingForm.Add("order[bill_address_attributes][hs_fiscal_code]", "")
	billingForm.Add("order[bill_address_attributes][id]", taskObject.BillAddID)
	billingForm.Add("order[use_billing]", "1")
	billingForm.Add("order[ship_address_attributes][id]", taskObject.ShipAddID)
	billingForm.Add("order[terms_and_conditions]", "no")
	billingForm.Add("order[terms_and_conditions]", "yes")
	billingForm.Add("commit", "Save and Continue")
	var updateAddressURL = taskObject.LocaleURL + "/checkout/update/address"
	var billingRequest, _ = http.NewRequest("POST", updateAddressURL, strings.NewReader(billingForm.Encode()))
	billingRequest.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	billingRequest.Header.Set("Accept-Language", "en-US,en;q=0.5")
	billingRequest.Header.Set("Referer", taskObject.CurrentURL)
	billingRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	billingRequest.Header.Set("DNT", "1")
	billingRequest.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(billingRequest)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "setting billing request")
		taskSetBilling(taskObject)
	} else {
		var loadedHTML, _ = goquery.NewDocumentFromResponse(reqResp)
		var taskCheckoutState, _ = loadedHTML.Find("#checkout").Attr("class")
		var stateLock, _ = loadedHTML.Find("#order_state_lock_version").Attr("value")
		var shippingID, _ = loadedHTML.Find("#order_shipments_attributes_0_id").Attr("value")
		var shippingRateID, _ = loadedHTML.Find(`*[name="order[shipments_attributes][0][selected_shipping_rate_id]"]`).Attr("value")
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskSetBilling(taskObject)
		} else {
			if reqResp.StatusCode == 200 && taskCheckoutState == "checkout_state_delivery" && len(stateLock) > 0 && len(shippingID) > 0 && len(shippingRateID) > 0 {
				taskObject.StateLock = stateLock
				taskObject.ShipID = shippingID
				taskObject.ShipRateID = shippingRateID
				taskObject.PreviousURL = taskObject.CurrentURL
				taskObject.CurrentURL = reqResp.Request.URL.String()
				riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
				setLastRxRun(taskObject.Client)
				cmdLog(taskObject.TaskNum, "success", "set billing information")
				taskSetDelivery(taskObject)
			} else if reqResp.Request.URL.String() == taskObject.LocaleURL+"/cart" {
				cmdLog(taskObject.TaskNum, "error", "item oos")
				//HANDLE OOS
			} else {
				cmdLog(taskObject.TaskNum, "error", "could not post billing information, retrying")
				time.Sleep(200 * time.Millisecond)
				taskSetBilling(taskObject)
			}
		}
	}
}

func taskSetDelivery(taskObject *taskOW) {
	var deliveryForm = url.Values{}
	deliveryForm.Set("utf8", "✓")
	deliveryForm.Set("_method", "patch")
	deliveryForm.Set("authenticity_token", taskObject.CSRFToken)
	deliveryForm.Set("order[state_lock_version]", taskObject.StateLock)
	deliveryForm.Set("order[shipments_attributes][0][selected_shipping_rate_id]", taskObject.ShipRateID)
	deliveryForm.Set("order[shipments_attributes][0][id]", taskObject.ShipID)
	deliveryForm.Set("commit", "Save and Continue")

	var updateDeliveryURL = taskObject.LocaleURL + "/checkout/update/delivery"
	var deliveryRequest, _ = http.NewRequest("POST", updateDeliveryURL, strings.NewReader(deliveryForm.Encode()))
	deliveryRequest.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	deliveryRequest.Header.Set("Accept-Language", "en-US,en;q=0.5")
	deliveryRequest.Header.Set("Referer", taskObject.CurrentURL)
	deliveryRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deliveryRequest.Header.Set("DNT", "1")
	deliveryRequest.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(deliveryRequest)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "setting delivery request")
		taskSetDelivery(taskObject)
	} else {
		var loadedHTML, _ = goquery.NewDocumentFromResponse(reqResp)
		var taskCheckoutState, _ = loadedHTML.Find("#checkout").Attr("class")
		var GestpaySelection = loadedHTML.Find(".gestpay-data")
		var orderAmount, _ = GestpaySelection.Attr("data-amount")
		var merchantID, _ = GestpaySelection.Attr("data-merchant")
		var orderID, _ = GestpaySelection.Attr("data-transaction")
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskSetDelivery(taskObject)
		} else if taskCheckoutState == "checkout_state_payment" && len(orderAmount) > 0 && len(merchantID) > 0 && len(orderID) > 0 {
			taskObject.OrderAmount = orderAmount
			taskObject.MerchantID = merchantID
			taskObject.OrderID = orderID
			taskObject.PreviousURL = taskObject.CurrentURL
			taskObject.CurrentURL = reqResp.Request.URL.String()
			riskified.SolveRX(taskObject.RXCookieID, taskObject.RXSessionID, shopURL, shopURL, taskObject.PreviousURL, taskObject.CurrentURL, taskObject.UserAgent, riskifiedAPI, taskObject.Client)
			setLastRxRun(taskObject.Client)
			cmdLog(taskObject.TaskNum, "success", "set delivery information")
			taskGetPaymentToken(taskObject)
		} else if reqResp.Request.URL.String() == taskObject.LocaleURL+"/cart" {
			cmdLog(taskObject.TaskNum, "error", "item oos")
			//HANDLE OOS
		} else {
			cmdLog(taskObject.TaskNum, "error", "could not post delivery information, retrying")
			time.Sleep(200 * time.Millisecond)
			taskSetDelivery(taskObject)
		}
	}
}

func taskGetPaymentToken(taskObject *taskOW) {
	var tokenForm = url.Values{}
	tokenForm.Add("transaction", taskObject.OrderID)
	tokenForm.Add("amount", taskObject.OrderAmount)
	tokenForm.Add("beacon_session_id", taskObject.RXSessionID)

	var getTokenURL = taskObject.LocaleURL + "/checkout/payment/get_token.json"
	var getTokenReq, _ = http.NewRequest("POST", getTokenURL, strings.NewReader(tokenForm.Encode()))
	getTokenReq.Header.Set("Accept", "application/json")
	getTokenReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	getTokenReq.Header.Set("Referer", taskObject.CurrentURL)
	getTokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	getTokenReq.Header.Set("DNT", "1")
	getTokenReq.Header.Set("User-Agent", taskObject.UserAgent)
	getTokenReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	var reqResp, reqErr = taskObject.Client.Do(getTokenReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "getting payment token")
		taskGetPaymentToken(taskObject)
	} else {
		defer reqResp.Body.Close()
		var respByteArray, _ = ioutil.ReadAll(reqResp.Body)
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskGetPaymentToken(taskObject)
		} else if reqResp.StatusCode == 200 {
			var tokenJSON = gjson.ParseBytes(respByteArray)
			var paymentToken = tokenJSON.Get("token").String()
			if len(paymentToken) > 0 {
				cmdLog(taskObject.TaskNum, "success", "got payment token")
				taskGetPaymentFrame(taskObject, paymentToken)
			} else {
				cmdLog(taskObject.TaskNum, "error", "could not get payment token, retrying")
				time.Sleep(200 * time.Millisecond)
				taskGetPaymentToken(taskObject)
			}
		}
	}
}

func taskGetPaymentFrame(taskObject *taskOW, paymentToken string) {
	var paymentFormReq, _ = http.NewRequest("GET", paymentFrameURL, nil)
	var formQuery = paymentFormReq.URL.Query()
	formQuery.Add("a", taskObject.MerchantID)
	formQuery.Add("b", paymentToken)
	formQuery.Add("MerchantUrl", taskObject.CurrentURL)
	paymentFormReq.URL.RawQuery = formQuery.Encode()
	var reqResp, reqErr = taskObject.Client.Do(paymentFormReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "getting payment form")
		taskGetPaymentFrame(taskObject, paymentToken)
	} else {
		var loadedFrame, _ = goquery.NewDocumentFromResponse(reqResp)
		var tokenViewState, _ = loadedFrame.Find("#__VIEWSTATE").Attr("value")
		var tokenViewStateGen, _ = loadedFrame.Find("#__VIEWSTATEGENERATOR").Attr("value")
		var tokenEventValidation, _ = loadedFrame.Find("#__EVENTVALIDATION").Attr("value")
		var pulledToken, _ = loadedFrame.Find(`#inputString`).Attr("value")
		if paymentToken == pulledToken && len(tokenViewState) > 0 && len(tokenViewStateGen) > 0 && len(tokenEventValidation) > 0 {
			cmdLog(taskObject.TaskNum, "success", "got payment params")
			taskObject.PaymentFrameURL = reqResp.Request.URL.String()
			taskPostPaymentFrame(taskObject, pulledToken, tokenViewState, tokenViewStateGen, tokenEventValidation)
		} else {
			cmdLog(taskObject.TaskNum, "error", "could not get payment params")
			taskGetPaymentFrame(taskObject, paymentToken)
		}
	}
}

func taskPostPaymentFrame(taskObject *taskOW, paymentToken, tokenViewState, tokenViewStateGen, tokenEventValidation string) {
	var paymentForm = url.Values{}
	paymentForm.Add("__VIEWSTATE", tokenViewState)
	paymentForm.Add("__VIEWSTATEGENERATOR", tokenViewStateGen)
	paymentForm.Add("__EVENTVALIDATION", tokenEventValidation)
	paymentForm.Add("cardnumber", taskObject.TaskInfo.Get("checkoutInformation.cardInformation.cardNumber").String())
	paymentForm.Add("cardExpiryMonth", taskObject.TaskInfo.Get("checkoutInformation.cardInformation.expMonth").String())
	paymentForm.Add("cardExpiryYear", taskObject.TaskInfo.Get("checkoutInformation.cardInformation.expYear").String())
	paymentForm.Add("cvv", taskObject.TaskInfo.Get("checkoutInformation.cardInformation.cardCVV").String())
	paymentForm.Add("buyerName", "undefined")
	paymentForm.Add("buyerEMail", "undefined")
	paymentForm.Add("inputString", paymentToken)
	paymentForm.Add("pares", "")
	paymentForm.Add("logPostData", "")
	paymentForm.Add("shopLogin", "")
	var postPaymentReq, _ = http.NewRequest("POST", taskObject.PaymentFrameURL, strings.NewReader(paymentForm.Encode()))
	postPaymentReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postPaymentReq.Header.Set("User-Agent", taskObject.UserAgent)
	var reqResp, reqErr = taskObject.Client.Do(postPaymentReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "posting payment form")
		taskPostPaymentFrame(taskObject, paymentToken, tokenViewState, tokenViewStateGen, tokenEventValidation)

	} else {
		var paymentFormResp, _ = goquery.NewDocumentFromResponse(reqResp)
		var delayedResult = paymentFormResp.Find("#form1 > script").Text()
		var unformattedToken = strings.Split(delayedResult, "')//]]>")
		var checkoutToken = strings.Split(unformattedToken[0], "delayedSendResult('0','','','','")[1]
		if len(checkoutToken) > 0 {
			cmdLog(taskObject.TaskNum, "success", "got final checkout token")
			taskProcessCheckoutToken(taskObject, checkoutToken)
		} else {
			cmdLog(taskObject.TaskNum, "error", "could not get final checkout token, retrying")
			taskPostPaymentFrame(taskObject, paymentToken, tokenViewState, tokenViewStateGen, tokenEventValidation)
		}
	}
}

func taskProcessCheckoutToken(taskObject *taskOW, checkoutToken string) {
	var processTokenForm = url.Values{}
	processTokenForm.Add("token", checkoutToken)

	var processTokenReq, _ = http.NewRequest("POST", processTokenURL, strings.NewReader(processTokenForm.Encode()))
	processTokenReq.Header.Set("Accept", "application/json")
	processTokenReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	processTokenReq.Header.Set("Referer", taskObject.CurrentURL)
	processTokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	processTokenReq.Header.Set("DNT", "1")
	processTokenReq.Header.Set("User-Agent", taskObject.UserAgent)
	processTokenReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	var reqResp, reqErr = taskObject.Client.Do(processTokenReq)
	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "submitting payment token")
		taskProcessCheckoutToken(taskObject, checkoutToken)
	} else {
		defer reqResp.Body.Close()
		var respByteArray, _ = ioutil.ReadAll(reqResp.Body)
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskSetBilling(taskObject)
		} else {
			var tokenRespJSON = gjson.ParseBytes(respByteArray)
			if tokenRespJSON.Get("redirect").Exists() {
				var orderRedirect = tokenRespJSON.Get("redirect").String()
				taskProcessCheckoutRedirect(taskObject, orderRedirect)
			} else {
				cmdLog(taskObject.TaskNum, "error", fmt.Sprintf("could not submit order - %v", tokenRespJSON.Get("error").String()))
				wg.Done()
			}
		}
	}
}

func taskProcessCheckoutRedirect(taskObject *taskOW, orderRedirect string) {
	var redirectURL = taskObject.LocaleURL + orderRedirect
	var redirectReq, _ = http.NewRequest("GET", redirectURL, nil)
	redirectReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	redirectReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	redirectReq.Header.Set("Referer", taskObject.CurrentURL)
	redirectReq.Header.Set("DNT", "1")
	redirectReq.Header.Set("User-Agent", taskObject.UserAgent)

	var reqResp, reqErr = taskObject.Client.Do(redirectReq)

	if reqErr != nil {
		cmdLog(taskObject.TaskNum, "error", reqErr.Error())
		cmdLog(taskObject.TaskNum, "retrying", "redirecting to order")
		taskProcessCheckoutRedirect(taskObject, orderRedirect)
	} else {
		defer reqResp.Body.Close()
		if cloudflare.CheckRestricted(reqResp) != "NO_CHALLENGE" {
			taskCFHandler(taskObject)
			taskSetBilling(taskObject)
		} else {
			cmdLog(taskObject.TaskNum, "success", fmt.Sprintf("order complete - %v", strings.Split(reqResp.Request.URL.String(), "/")[6]))
			wg.Done()
		}
	}
}
