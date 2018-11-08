package util

import (
	"encoding/json"
	"net/http"
	"fmt"
	"io/ioutil"
	"os"
)

func LoadConfiguration(apiBackend string, apiDomain string) *List {
	expressions := new(List)

	response, err := http.Get(fmt.Sprintf("%s/getconfig.php", apiBackend))
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
		fmt.Printf("%s\n", string(contents))

		// Don't export this.
		var configuration struct {
			Peers []string
			Routes []string
		}

		if err = json.Unmarshal(contents, &configuration); err != nil {
			fmt.Printf("Configuration broken: %s", err)
		}

		for k := range configuration.Routes {
			fmt.Printf("K : %s", k)	
		}

		fmt.Printf("%+v", configuration)
	}

	return expressions
}

