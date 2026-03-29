package api

import (
	"fmt"
	"net/http"
	"time"
)

const apiRequestTimeout = 10 * time.Second
const APIUserAgent = "vinth/0.1.0 (vlocitize@gmail.com)"

var sharedHTTPClient = &http.Client{
	Timeout: apiRequestTimeout,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
	},
}

func doWith429Retry(req *http.Request, retries int) (*http.Response, error) {
	if retries < 1 {
		retries = 1
	}

	var (
		resp *http.Response
		err  error
	)

	for i := 0; i < retries; i++ {
		resp, err = sharedHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		resp.Body.Close()
		if i < retries-1 {
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}

	if resp != nil {
		return resp, nil
	}

	return nil, fmt.Errorf("request failed after %d attempts", retries)
}

func setAPIUserAgent(req *http.Request) {
	req.Header.Set("User-Agent", APIUserAgent)
}
