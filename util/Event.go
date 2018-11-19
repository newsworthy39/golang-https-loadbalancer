package util

import (
	"fmt"
	"net/http"
	"os"
)


type Event struct {
	EventID   int
	EventData string
}
func NewEvent(EventID int, EventData string) *Event {
	return &Event{EventID, EventData}
}

func (a *JSONApiConfiguration) SendEvent(e *Event) error {

	path := fmt.Sprintf("%s/event", a.JSONBackend)

	req, err := http.NewRequest("POST", path, nil)

	if err != nil {
		return err
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
		if resp.StatusCode != 201  {
			fmt.Printf("Could not read reply from the API. Result: %d", resp.StatusCode);
		}

	}
	return nil
}


