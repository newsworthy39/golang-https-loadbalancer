package util

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
)

type ApiConfiguration struct {
	apiDomain string
	apiBackend string
	secret string
	accessKey string
}

type Backend struct {
	Backend string
}

type route struct {
	Type          string
	Path          string
	Method	      string
	Backends      []Backend
}

func NewApiConfiguration(apiDomain string, apiBackend string, secret string, accessKey string) *ApiConfiguration {
	return &ApiConfiguration{apiDomain, apiBackend, secret, accessKey}
}

func (a *ApiConfiguration) LoadConfigurationFromRESTApi() []route {

	path := fmt.Sprintf("%s/loadbalancer", a.apiBackend)

	req, err := http.NewRequest("GET", path, nil)
	routes := []route{}

	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	} else {
		req.Header.Set("AccessKey", a.accessKey)
		req.Header.Set("Secret", a.secret)
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("%s", err)
			os.Exit(1)
		}

		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			fmt.Printf("Could not read configuration from API");
			os.Exit(1)
		}

		contents, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("PPanic %s", err)
			os.Exit(1)
		}

		if err = json.Unmarshal(contents, &routes); err != nil {
			fmt.Printf("Configuration broken: %s", err)
		}
	}

	return routes
}


