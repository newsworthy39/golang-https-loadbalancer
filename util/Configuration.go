package util

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
)

type config struct {
	Peers  []string
	Routes []string
}

type route struct {
	Type          string
	Path          string
	Loadbalancing string
	Backends      []string
}

func DoConfiguration(path string) config {

	response, err := http.Get(path)
	config := config{}

	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	} else {
		defer response.Body.Close()
		contents, err := ioutil.ReadAll(response.Body)
		if err != nil {
			fmt.Printf("PPanic %s", err)
			os.Exit(1)
		}

		if err = json.Unmarshal(contents, &config); err != nil {
			fmt.Printf("Configuration broken: %s", err)
		}
	}

	return config
}

func DoRoute(path string) route {

	response, err := http.Get(path)
	route := route{}

	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	} else {
		defer response.Body.Close()
		contents, err := ioutil.ReadAll(response.Body)
		if err != nil {
			fmt.Printf("PPanic %s", err)
			os.Exit(1)
		}

		if err = json.Unmarshal(contents, &route); err != nil {
			fmt.Printf("Configuration broken: %s", err)
		}
	}

	return route
}

