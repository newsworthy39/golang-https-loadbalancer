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

func doConfiguration(path string, body *config) {

	response, err := http.Get(path)
	defer response.Body.Close()

	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	} else {
		contents, err := ioutil.ReadAll(response.Body)
		if err != nil {
			fmt.Printf("PPanic %s", err)
			os.Exit(1)
		}

		if err = json.Unmarshal(contents, &body); err != nil {
			fmt.Printf("Configuration broken: %s", err)
		}
	}
}

func doRoute(path string, body *route) {

	response, err := http.Get(path)
	defer response.Body.Close()

	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	} else {
		contents, err := ioutil.ReadAll(response.Body)
		if err != nil {
			fmt.Printf("PPanic %s", err)
			os.Exit(1)
		}

		if err = json.Unmarshal(contents, &body); err != nil {
			fmt.Printf("Configuration broken: %s", err)
		}
	}
}

func LoadConfiguration(apiBackend string, apiDomain string) *List {
	expressions := new(List)

	// Don't export this.
	configuration := config{}

	doConfiguration(fmt.Sprintf("%s/getconfig.php", apiBackend), &configuration)

	route := route{}
	for _, element := range configuration.Routes {
		doRoute(element, &route)
		fmt.Printf("%+v\n", route)
	}

	return expressions
}
