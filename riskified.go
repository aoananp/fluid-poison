/*
  Created on Wed Sep 26 2018
  Copyright (c) 2018 Hasan Gondal
*/

package riskified

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	beaconURL = "https://beacon.riskified.com/"
	clientURL = "https://c.riskified.com/client_infos.json"
	imageURL  = "https://img.riskified.com/img/image-l.gif"
)

var (
	localClient = &http.Client{}
)

func generateRXTimestamp() string {
	rand.Seed(time.Now().UnixNano())
	var currentTime = time.Now().UnixNano() / 1e6
	return strconv.FormatFloat(float64(currentTime), 'f', -1, 64) + strconv.FormatFloat(rand.Float64(), 'f', -1, 64)
}

func riskifiedImgRequest(cookieID string, pageID string, sessionID string, shopURL string, beaconTS string, refererURL string, userAgent string, rxClient *http.Client) {
	var imgReq, _ = http.NewRequest("GET", imageURL, nil)
	var imgQuery = imgReq.URL.Query()
	imgQuery.Add("t", generateRXTimestamp())
	imgQuery.Add("c", cookieID)
	imgQuery.Add("p", pageID)
	imgQuery.Add("a", sessionID)
	imgQuery.Add("o", shopURL)
	imgQuery.Add("rt", beaconTS)
	imgReq.URL.RawQuery = imgQuery.Encode()

	imgReq.Header.Set("Accept", "*/*")
	imgReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	imgReq.Header.Set("Referer", refererURL)
	imgReq.Header.Set("User-Agent", userAgent)

	imgResp, imgReqError := rxClient.Do(imgReq)
	if imgReqError != nil {
		riskifiedImgRequest(cookieID, pageID, sessionID, shopURL, beaconTS, refererURL, userAgent, rxClient)
	}
	defer imgResp.Body.Close()
}

func getBeaconTimestamp(shopURL, sessionID, userAgent string, rxClient *http.Client) string {
	var beaconRequest, _ = http.NewRequest("GET", beaconURL, nil)
	var beaconQuery = beaconRequest.URL.Query()
	beaconQuery.Add("shop", shopURL)
	beaconQuery.Add("sid", sessionID)
	beaconRequest.URL.RawQuery = beaconQuery.Encode()

	beaconRequest.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8")
	beaconRequest.Header.Set("Accept-Language", "en-US,en;q=0.5")
	beaconRequest.Header.Set("Cache-Control", "max-age=0")
	beaconRequest.Header.Set("User-Agent", userAgent)

	beaconResp, beaconReqError := rxClient.Do(beaconRequest)
	if beaconReqError != nil {
		return "BeaconRequestError"
	}
	defer beaconResp.Body.Close()
	var respBytes, _ = ioutil.ReadAll(beaconResp.Body)
	var respBody = string(respBytes[:])
	var firstSplitArray = strings.Split(respBody, `getYyRxId3() { return "`)
	var beaconTimestamp = strings.Split(firstSplitArray[1], `";}`)[0]
	if beaconTimestamp != "" {
		return beaconTimestamp
	}
	return "BeaconRequestError"
}

//SolveRX completes the fraud risk challenge
func SolveRX(cookieID, sessionID, shopURL, siteURL, refererURL, currentURL, userAgent, riskifiedAPI string, rxClient *http.Client) {
	var requestTimeSlice []int
	var timezoneOffset = grabTimezoneOffset()
	var pageID = getPageID(riskifiedAPI)
	var beaconTimestamp = getBeaconTimestamp(shopURL, sessionID, userAgent, rxClient)
	if pageID != "ErrorAPI" && beaconTimestamp != "BeaconRequestError" {
		for i := 0; i < 6; i++ {
			var start = time.Now()
			riskifiedImgRequest(cookieID, pageID, sessionID, shopURL, beaconTimestamp, refererURL, userAgent, rxClient)
			var requestDuration = int(time.Since(start)) / 1e6
			requestTimeSlice = append(requestTimeSlice, requestDuration)
		}
		var lowestLatency = getLowestValue(requestTimeSlice)
		var clientRXReq, _ = http.NewRequest("GET", clientURL, nil)
		var queryRX = clientRXReq.URL.Query()

		queryRX.Add("lat", strconv.Itoa(lowestLatency))
		queryRX.Add("timezone", strconv.Itoa(timezoneOffset))
		queryRX.Add("cart_id", sessionID)
		queryRX.Add("shop_id", shopURL)
		queryRX.Add("referrer", refererURL)
		queryRX.Add("href", currentURL)
		queryRX.Add("riskified_cookie", cookieID)
		queryRX.Add("color_depth", "24")
		queryRX.Add("page_id", pageID)
		queryRX.Add("hardware_concurrency", "8")
		queryRX.Add("has_touch", "false")
		queryRX.Add("debug_print", "false")
		queryRX.Add("console_error", "console.memory is undefined")
		queryRX.Add("battery_error", "Error getBattery()")
		queryRX.Add("initial_cookie_state_0", "http")
		queryRX.Add("initial_cookie_state_1", "local")
		queryRX.Add("initial_cookie_state_2", "session")

		clientRXReq.URL.RawQuery = queryRX.Encode()

		clientRXReq.Header.Set("Accept", "*/*")
		clientRXReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
		clientRXReq.Header.Set("Origin", siteURL)
		clientRXReq.Header.Set("Referer", refererURL)
		clientRXReq.Header.Set("User-Agent", userAgent)

		var clientRXResp, clientRXError = rxClient.Do(clientRXReq)
		if clientRXError != nil {
			fmt.Println("[" + time.Now().Format("15:04:05.000") + "] [FLUID] [RISKIFIED] [####] [ERROR] [RETRYING]")
			SolveRX(cookieID, sessionID, shopURL, siteURL, refererURL, currentURL, userAgent, riskifiedAPI, rxClient)
		} else {
			defer clientRXResp.Body.Close()
		}
	} else {
		fmt.Println("[" + time.Now().Format("15:04:05.000") + "] [FLUID] [RISKIFIED] [####] [ERROR] [RETRYING]")
		SolveRX(cookieID, sessionID, shopURL, siteURL, refererURL, currentURL, userAgent, riskifiedAPI, rxClient)
	}
}

func getPageID(riskifiedAPI string) string {
	var reqAPI, _ = http.NewRequest("GET", fmt.Sprintf("%v/api/page", riskifiedAPI), nil)
	reqAPI.Header.Set("Accept", "application/json")
	var respAPI, errAPI = localClient.Do(reqAPI)
	if errAPI != nil {
		return "ErrorAPI"
	}
	defer respAPI.Body.Close()
	var respBytes, _ = ioutil.ReadAll(respAPI.Body)
	var pageID = gjson.ParseBytes(respBytes).Get("page").String()
	return pageID
}

//CheckAPIStatus returns a boolean is based on whether the local API is online
func CheckAPIStatus(riskifiedAPI string) bool {
	var reqAPI, _ = http.NewRequest("GET", riskifiedAPI, nil)
	reqAPI.Header.Set("Accept", "application/json")
	var respAPI, errAPI = localClient.Do(reqAPI)
	if errAPI != nil {
		return false
	}
	defer respAPI.Body.Close()
	var respBytes, _ = ioutil.ReadAll(respAPI.Body)
	if gjson.ParseBytes(respBytes).Get("api").String() == "online" {
		return true
	}
	return false
}

func getLowestValue(intArr []int) int {
	var minimumValue = intArr[0]
	for _, v := range intArr {
		if v < minimumValue {
			minimumValue = v
		}
	}
	return minimumValue
}

func grabTimezoneOffset() int {
	var currentTime = time.Now()
	var _, offsetSeconds = currentTime.Zone()
	return offsetSeconds / 60
}

//GetSessionCookieSet returns a slice with either a session & cookie set or an error
func GetSessionCookieSet(riskifiedAPI string) []string {
	var reqAPI, _ = http.NewRequest("GET", fmt.Sprintf("%v/api", riskifiedAPI), nil)
	reqAPI.Header.Set("Accept", "application/json")
	var respAPI, errAPI = localClient.Do(reqAPI)
	if errAPI != nil {
		return []string{"ErrorAPI"}
	}
	defer respAPI.Body.Close()
	var respBytes, _ = ioutil.ReadAll(respAPI.Body)
	return []string{gjson.ParseBytes(respBytes).Get("session").String(), gjson.ParseBytes(respBytes).Get("cookie").String()}
}
