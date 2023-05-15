package network

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/sendgrid/rest"
)

func Post(url string, body []byte, headers map[string]string) (resp *rest.Response, err error) {

	return RequestBoy(url, body, headers, rest.Post)
}

func Put(url string, body []byte, headers map[string]string) (resp *rest.Response, err error) {

	return RequestBoy(url, body, headers, rest.Put)
}

func PostForQueryParam(url string, queryParams map[string]string, headers map[string]string) (resp *rest.Response, err error) {

	return RequestBoyForQueryParam(url, queryParams, headers, rest.Post)
}

func RequestBoyForQueryParam(url string, queryParams map[string]string, headers map[string]string, method rest.Method) (resp *rest.Response, err error) {

	request := rest.Request{
		Method:      method,
		BaseURL:     url,
		QueryParams: queryParams,
		Headers:     headers,
	}
	response, err := rest.API(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}
func RequestBoy(url string, body []byte, headers map[string]string, method rest.Method) (resp *rest.Response, err error) {

	request := rest.Request{
		Method:  method,
		BaseURL: url,
		Body:    body,
		Headers: headers,
	}
	response, err := rest.API(request)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func Get(url string, queryParams map[string]string, headers map[string]string) (resp *rest.Response, err error) {

	request := rest.Request{
		Method:      rest.Get,
		BaseURL:     url,
		Headers:     headers,
		QueryParams: queryParams,
	}
	response, err := rest.API(request)
	if err != nil {

		return nil, err
	}

	return response, nil
}

func GetJson(url string, queryParams map[string]string, headers map[string]string) (byts []byte, err error) {

	request := rest.Request{
		Method:      rest.Get,
		BaseURL:     url,
		Headers:     headers,
		QueryParams: queryParams,
	}
	response, err := rest.API(request)
	if err != nil {

		return nil, err
	}

	return []byte(response.Body), nil
}

func PostForWWWFormForBytres(urlStr string, params map[string]string, headers map[string]string) ([]byte, error) {
	data := url.Values{}
	for key, value := range params {
		data.Set(key, value)
	}
	queryStr := ""

	for key, value := range params {
		queryStr = fmt.Sprintf("%s=%s&%s", key, value, queryStr)
	}
	if len(queryStr) > 0 {
		queryStr = queryStr[0 : len(queryStr)-1]
	}
	request, err := http.NewRequest("POST", urlStr, strings.NewReader(queryStr))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var resp *http.Response
	resp, err = http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return body, errors.New(fmt.Sprintf("状态码：%d", resp.StatusCode))
	}
	return body, nil
}

func PostForWWWForm(urlStr string, params map[string]string, headers map[string]string) (map[string]interface{}, error) {
	body, err := PostForWWWFormForBytres(urlStr, params, headers)
	if err != nil {
		return nil, err
	}
	var resultMap map[string]interface{}
	err = wkutil.ReadJsonByByte(body, &resultMap)
	if err != nil {
		return nil, err
	}
	return resultMap, nil

}

func PostForWWWFormForAll(urlStr string, bodyData io.Reader, headers map[string]string) ([]byte, error) {
	request, err := http.NewRequest("POST", urlStr, bodyData)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var resp *http.Response
	resp, err = http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(fmt.Sprintf("状态码：%d", resp.StatusCode))
	}
	return body, nil
}

func PostForWWWFormReXML(urlStr string, params map[string]string, headers map[string]string) ([]byte, error) {

	data := url.Values{}
	for key, value := range params {
		data.Set(key, value)
	}
	queryStr := ""

	for key, value := range params {
		queryStr = fmt.Sprintf("%s=%s&%s", key, value, queryStr)
	}
	if len(queryStr) > 0 {
		queryStr = queryStr[0 : len(queryStr)-1]
	}
	request, err := http.NewRequest("POST", urlStr, strings.NewReader(queryStr))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var resp *http.Response
	resp, err = http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respData, err := ioutil.ReadAll(resp.Body)
	fmt.Println(string(respData))
	if err != nil {
		return []byte(""), err
	}
	return respData, nil

}
