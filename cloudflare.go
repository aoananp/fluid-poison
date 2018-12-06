/*
  Created on Fri Sep 28 2018
  Copyright (c) 2018 Hasan Gondal
*/

package cloudflare

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/robertkrimen/otto"

	"github.com/PuerkitoBio/goquery"
	"github.com/nuveo/anticaptcha"
)

var (
	acAPIKey = "fuckoffgetyourownkey"
)

// CheckRestricted returns challenge type.
func CheckRestricted(r *http.Response) string {
	var headerCF = strings.Contains(r.Header.Get("Server"), "cloudflare")
	var loadedHTML, _ = goquery.NewDocumentFromResponse(r)
	var _, captchaCheck = loadedHTML.Find("#challenge-form > script").Attr("data-sitekey")
	if r.StatusCode == 503 && headerCF {
		return "JAVASCRIPT_CHALLENGE"
	} else if r.StatusCode == 403 {
		if captchaCheck && headerCF {
			return "RECAPTCHA_CHALLENGE"
		} else if headerCF {
			return "BANNED_IP"
		}
	}
	return "NO_CHALLENGE"
}

func copyStrSlice(in []string) []string {
	var r = make([]string, 0, len(in))
	r = append(r, in...)
	return r
}

func copyHeader(header http.Header) http.Header {
	var m = make(map[string][]string)

	for k, v := range header {
		m[k] = copyStrSlice(v)
	}

	return m
}

func extractJS(body []byte, domain string) string {
	var r1, _ = regexp.Compile(`setTimeout\(function\(\){\s+(var s,t,o,p,b,r,e,a,k,i,n,g,f.+?\r?\n[\s\S]+?a\.value =.+?)\r?\n`)
	var r2, _ = regexp.Compile(`a\.value = (.+ \+ t\.length)`)
	var r3, _ = regexp.Compile(`\s{3,}[a-z](?: = |\.).+`)
	var r4, _ = regexp.Compile(`[\n\\']`)

	var r1Match = r1.FindSubmatch(body)

	if len(r1Match) != 2 {
		return ""
	}

	var js = string(r1Match[1])
	js = r2.ReplaceAllString(js, "$1")
	js = r3.ReplaceAllString(js, "")

	js = strings.Replace(js, "t.length", fmt.Sprintf("%d", len(domain)), -1)

	js = r4.ReplaceAllString(js, "")

	var lastSemicolon = strings.LastIndex(js, ";")
	if lastSemicolon >= 0 {
		js = js[:lastSemicolon]
	}

	return js
}

func parseChallenge(js string) string {
	var vm = otto.New()
	var result, _ = vm.Run(js)
	return result.String()
}

func solveJS(req *http.Request, res *http.Response, resBody []byte, clientCF *http.Client) []*http.Cookie {
	time.Sleep(4200 * time.Millisecond)
	var js = extractJS(resBody, req.URL.Host)
	var answer = strings.TrimSpace(parseChallenge(js))
	var vc, _ = regexp.Compile(`name="jschl_vc" value="(\w+)"`)
	var pass, _ = regexp.Compile(`name="pass" value="(.+?)"`)

	var vcMatch = vc.FindSubmatch(resBody)
	var passMatch = pass.FindSubmatch(resBody)

	if !(len(vcMatch) == 2 && len(passMatch) == 2) {
		return nil
	}

	var url, _ = url.Parse(fmt.Sprintf("%s://%s/cdn-cgi/l/chk_jschl", req.URL.Scheme, req.URL.Host))

	var query = url.Query()
	query.Set("jschl_vc", string(vcMatch[1]))
	query.Set("pass", string(passMatch[1]))
	query.Set("jschl_answer", answer)
	url.RawQuery = query.Encode()

	var nReq, err = http.NewRequest("GET", url.String(), nil)
	if err != nil {
		return nil
	}

	nReq.Header = copyHeader(res.Header)
	nReq.Header.Set("Referer", req.URL.String())
	nReq.Header.Set("User-Agent", req.Header.Get("User-Agent"))

	var reqResp, resErr = clientCF.Do(nReq)
	if resErr != nil {
		return nil
	}
	defer reqResp.Body.Close()
	var cookies = clientCF.Jar.Cookies(req.URL)

	for _, individualCookie := range cookies {
		individualCookie.HttpOnly = true
		individualCookie.Secure = false
	}

	return cookies
}

func solveReCaptcha(req *http.Request, clientCF *http.Client) []*http.Cookie {
	var res, err = clientCF.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	var captchaHTML, _ = goquery.NewDocumentFromResponse(res)
	var cfSiteKey, _ = captchaHTML.Find("#challenge-form > script").Attr("data-sitekey")
	var rayID, _ = captchaHTML.Find("#challenge-form > script").Attr("data-ray")
	if cfSiteKey == "" {

	}
	var acClient = &anticaptcha.Client{APIKey: acAPIKey}

	captchaSolution, captchaSolvingErr := acClient.SendRecaptcha(req.URL.String(), cfSiteKey)
	if captchaSolvingErr != nil {
		return nil
	}

	var url, _ = url.Parse(fmt.Sprintf("%s://%s/cdn-cgi/l/chk_captcha", req.URL.Scheme, req.URL.Host))
	var query = url.Query()
	query.Set("id", rayID)
	query.Set("g-recaptcha-response", captchaSolution)
	url.RawQuery = query.Encode()

	var challengeRequest, _ = http.NewRequest("GET", url.String(), nil)
	challengeRequest.Header.Set("Referer", req.URL.String())
	challengeRequest.Header.Set("User-Agent", req.Header.Get("User-Agent"))

	var reqResp, resErr = clientCF.Do(challengeRequest)
	if resErr != nil {
		return nil
	}
	defer reqResp.Body.Close()
	var cookies = clientCF.Jar.Cookies(req.URL)

	for _, individualCookie := range cookies {
		individualCookie.HttpOnly = true
		individualCookie.Secure = false
	}

	return cookies
}

//GetTokens attempts to solve the Cloudflare challenege and returns a cookie slice or an error
func GetTokens(siteURL, userAgent string, clientCF *http.Client) []*http.Cookie {
	var jarCF, _ = cookiejar.New(nil)
	clientCF.Jar = jarCF
	var req, _ = http.NewRequest("GET", siteURL, nil)

	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("DNT", "1")
	req.Header.Set("User-Agent", userAgent)

	var res, resErr = clientCF.Do(req)
	if resErr != nil {
		fmt.Println("["+getTimestamp()+"]", "[FLUID]", "[CLOUDFLARE]", "[####]", "[REQUEST ERROR]")
		return nil
	}
	defer res.Body.Close()

	var resBody, _ = ioutil.ReadAll(res.Body)
	var CloudFlareStatus = CheckRestricted(res)

	if CloudFlareStatus == "JAVASCRIPT_CHALLENGE" {
		return solveJS(req, res, resBody, clientCF)
	} else if CloudFlareStatus == "RECAPTCHA_CHALLENGE" {
		return solveReCaptcha(req, clientCF)
	}

	return nil
}

//IsRestricted returns the type of Cloudflare challenge (if available)
func IsRestricted(siteURL, userAgent string, clientCF *http.Client) string {
	var req, _ = http.NewRequest("GET", siteURL, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("DNT", "1")
	req.Header.Set("User-Agent", userAgent)

	var res, resErr = clientCF.Do(req)
	if resErr != nil {
		fmt.Println("["+getTimestamp()+"]", "[FLUID]", "[CLOUDFLARE]", "[####]", "[REQUEST ERROR]")
		return "ReqError"
	}
	defer res.Body.Close()
	return CheckRestricted(res)
}

func getTimestamp() string {
	return time.Now().Format("15:04:05.000")
}
