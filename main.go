package main

import (
	"encoding/json"
	"fmt"
	zmq "github.com/pebbe/zmq4"
//	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type Statistics struct {
	// This follows statistics, pr.
	Requests   int64
	NoRoutes   int64
	NoBackends int64
	StartTime  int64
	EndTime    int64
}

type Application struct {
	Next        *Application
	Prev        *Application
	End         *Application
	Application string
	Targets     []string
	Healthcheck string
	Routes      []*regexp.Regexp
	Root       bool
	Order       int

	// Stats
	Stats Statistics

	// Strategy
	Strategy string
}

type ApplicationRequest struct {
	Application string
	Target      string
	Healthcheck string
	Routes      []string
	Order       int

	// Strategy
	Strategy string

}

func (p *Application) GetApplication(application string) *Application {
	if p.Application == application {
		return p
	} else {
		if p.IsEnd() == false {
			return p.Next.GetApplication(application)
		} else {
			return nil
		}
	}
}

func (p *Application) GetFirst() *Application {
	if p.IsRoot() {
		return p
	} else {
		return p.Prev.GetFirst()
	}
}

func (p *Application) HasApplication(application string) bool {
	if p.Application == application {
		return true
	} else {
		if p.IsEnd() == false {
			return p.Next.HasApplication(application)
		} else {
			return false
		}
	}
}

func (p *Application) Length() int {
	sum := 0
	if p.IsEnd() == false {
		sum = sum + p.Length()
	} else {
		return 1
	}
	return sum
}

func (p *Application) CreateApplication(application ApplicationRequest) bool {
	if p.IsRoot() == true && p.Next != nil {
		return p.Next.CreateApplication(application)
	}

	// middle-insertion.
	if application.Order > p.Order {

		app := &Application{
			Application: application.Application,
			Targets:     []string{application.Target},
			Healthcheck: application.Healthcheck,
			Stats:       Statistics{StartTime: time.Now().Unix()},
			Order:       application.Order,
			Root:        false,
			Strategy:    application.Strategy,
		}

		app.SetRoutes(application.Routes)

		fmt.Printf("Application %s created, in the middle, w/ order %d.\n", app.Application, app.Order)

		p.Prev.Next = app
		app.Next = p.Next

		return true

		// tail-insertion, lwoer precedence
	} else if p.IsEnd() {

		app := &Application{
			Application: application.Application,
			Targets:     []string{application.Target},
			Healthcheck: application.Healthcheck,
			Stats:       Statistics{StartTime: time.Now().Unix()},
			Order:       application.Order,
			Root :       false,
			Strategy:    application.Strategy,
		}

		app.SetRoutes(application.Routes)

		p.Next = app
		app.Prev = p

		// Allways, set end, to the latest-entry.
		p.End = app

		// Log a message about it.
		fmt.Printf("Application %s created, at the end w/ order %d \n", app.Application, app.Order)

		return true

	} else {
		return p.Next.CreateApplication(application)
	}
}

//
// RemoveEmptyApplications
// Applications, containing 0 registered backends,
// are removed.
// returns: nothing.
func (p *Application) RemoveEmptyApplication() {

	app := p.GetFirst()
	for app.IsEnd() == false {
		app = app.Next

		if len(app.Targets) < 1 {

			// bye! p.Next.Prev will be p.Prev
			// bye! p.Next wil be p.Next.Next
			if app.IsEnd() == false {
				app.Next.Prev = app.Prev
			}

			if p.IsRoot() == false {
				app.Prev.Next = app.Next
			}

			if app.IsEnd() == true && app.IsRoot() == false {
				app.Prev.Next = nil
			}


			fmt.Printf("RemoveAmptyApplication() %s IsEnd(): %b, IsRoot: %b, len(Targets): %d.\n",
				app.Application, app.IsEnd(), app.IsRoot(), len(app.Targets))

		}
	}
}

func (p *Application) IsRoot() bool {
	return p.Root
}

func (p *Application) IsEnd() bool {
	return p.Next == nil
}

func (p *Application) IsEmpty() bool {
	return p.IsRoot() && p.IsEnd()
}

func (p *Application) check() {
	for _, val := range p.Targets {
		res, err := http.Head(fmt.Sprintf("%s%s", val, p.Healthcheck))
		if err != nil {
			log.Printf("Error = %v\n", err)
			p.Leave(p.Application, val)
			return
		}
		//check, err := ioutil.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			log.Printf("Error = %v\n", err)
		}
		fmt.Printf("check() performed on %s, with status %d.\n", fmt.Sprintf("%s%s", val, p.Healthcheck), res.StatusCode)
	}
}

func RandomStrategy(application *Application) int {
	return rand.Intn(len(application.Targets))
}

func RoundRobinStrategy(application *Application) int {
	r := application.Stats.Requests
	return int(r) % len(application.Targets)
}

func SelectStrategy(application *Application) int {
	m := map[string]func(application *Application) int {
		"RoundRobinStrategy": RoundRobinStrategy,
		"RandomStrategy"    : RandomStrategy,
	}

	if len(application.Strategy) > 0 {
		return m[application.Strategy](application)
	} else {
		return m["RoundRobinStrategy"](application)
	}
}


func (p *Application) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	if p.IsEmpty() {

		// Error to flush the io-streams
		http.Error(res,
			fmt.Sprintf("This loadbalancer has not yet been configured."),
			http.StatusInternalServerError)
		return
	}

	if p.MatchRequestAgainstRoutes(req) && len(p.Targets) >= 1 {

		// Accouting.
		p.Stats.Requests++

		// pick a target.
		peer := p.Targets[SelectStrategy(p)]

		target, _ := url.Parse(peer)

		// logRequestPayload(requestPayload, url)

		// create the reverse proxy
		proxy := httputil.NewSingleHostReverseProxy(target)

		// Update the headers to allow for SSL redirection
		req.URL.Host = target.Host
		req.URL.Scheme = target.Scheme
		req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
		req.Host = target.Host

		// Note that ServeHttp is non blocking and uses a go routine under the hood
		proxy.ServeHTTP(res, req)

	} else {
		if p.IsEnd() == false {
			p.Next.ServeHTTP(res, req)
		} else {

			// Allways, log in root-application.
			root := p.GetFirst()

			if root.HasApplication("root") {
				t := root.GetApplication("root")

				// Accouting, only happens in root application
				t.Stats.NoRoutes++
			}

			// Error to flush the io-streams
			http.Error(res,
				fmt.Sprintf("This path has not received any configuration."),
				http.StatusInternalServerError)
			return

		}
	}
}

//
// Helpers
//
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func (p *Application) Join(application string, server string) bool {
	fmt.Printf("Join() Looking at %s, %s.\n", p.Application, server)
	if p.Application == application {
		p.Leave(application, server)
		p.Targets = append(p.Targets, server)
		fmt.Printf("Added server %s to %s, final result %s.\n", server, application, p.Targets)
		return true
	} else {
		if p.IsEnd() == false {
			return p.Next.Join(application, server)
		} else {
			return false
		}
	}

}

func (p *Application) Leave(application string, server string) {
	fmt.Printf("Leave() Looking at %s, %s.\n", p.Application, server)
	if p.Application == application {
		targets := []string{}
		for _, val := range p.Targets {
			if val != server {
				targets = append(targets, val)
			}
		}
		p.Targets = targets
		fmt.Printf("Deleted %s from application %s, final result %s.\n", server, application, p.Targets)

	} else {
		if p.IsEnd() == false {
			p.Next.Leave(application, server)
		} else {
			return
		}
	}
}

func (p *Application) SetHealthcheck(application string, healthcheck string) {
	fmt.Printf("SetHealthcheck() Looking at %s, %s.\n", application, healthcheck)
	if p.Application == application {
		p.Healthcheck = healthcheck
		fmt.Printf("set healthcheck %s in %s..\n", healthcheck, application)
	} else {
		if p.IsEnd() != false {
			p.Next.SetHealthcheck(application, healthcheck)
		}
	}
}

func (p *Application) SetRoutes(routes []string) bool {
	regexroutes := []*regexp.Regexp{}
	for _, val := range routes {
		r, _ := regexp.Compile(val)
		if r != nil {
			regexroutes = append(regexroutes, r)
		} else {
			fmt.Printf("Could not parse regex %q and add it to application %s.\n", val, p.Application)
			return false
		}
	}
	p.Routes = regexroutes

	fmt.Printf("SetRoutes %q with application %s.\n",
		p.Routes, p.Application)

	return true
}

func (p *Application) FindAndUpdateRoutes(application string, routes []string) bool {
	fmt.Printf("SetRoutes() Looking at %s, %q.\n", application, routes)
	if p.Application == application {
		p.SetRoutes(routes)
		return true
	} else {
		if p.IsEnd() != false {
			return p.Next.FindAndUpdateRoutes(application, routes)
		}
	}

	return false
}

func (p *Application) MatchRequestAgainstRoutes(req *http.Request) bool {
	for _, r := range p.Routes {
		if found := r.MatchString(req.URL.Path); found == true {
			return true
		}
	}
	return false
}

//
// hello.PerformStatsAssembly
// Collects and sends stats, to a endpoint, defined clearly
// and easily monitored.
//
func (p *Application) PerformStatsAssembly(duration string) {
	publisher, _ := zmq.NewSocket(zmq.PUB)
	defer publisher.Close()
	//	subscriber.Connect("tcp://localhost:5563")
	publisher.Bind(fmt.Sprintf("tcp://*:%s", getEnv("SEND", "5567")))
	fmt.Printf("Will broadcast stats on ZMQ, to /statistics/.\n")

	for {
		// The first, app is the root-node, as is such, empty.
		// This idiom, traverses the list, until end - outputting
		// statistics. We add the "EndTime" each time, to indicate, the
		// timeperiod.
		endTime := time.Now().Unix()

		app := p.GetFirst()

		for {

			// Insert endtime
			app.Stats.EndTime = endTime

			type wireStats struct {
				Application string
				Objects Statistics
			}

			group := wireStats { Application : app.Application, Objects : app.Stats }

			stats, err := json.Marshal(group)
			if err != nil {
				fmt.Printf("Error marshalling stats", err)
			}

			fmt.Printf("Stats from %s: Stats: %s\n", app.Application, stats)
			publisher.Send(fmt.Sprintf("/statistics/%s", app.Application), zmq.SNDMORE)
			publisher.Send(string(stats), 0)

			// Rset stats, use endTime, to get consistent time-reporting.
			app.Stats = Statistics{StartTime: endTime}

			// Advance, but only if not end
			if app.IsEnd() == true{
				break
			}
		 	app = app.Next
		}

		complex, _ := time.ParseDuration(duration)
		time.Sleep(complex)
	}

}

//
//  Pubsub envelope subscriber
//
func (p *Application) subscribe() {
	subscriber, _ := zmq.NewSocket(zmq.SUB)
	defer subscriber.Close()
	//	subscriber.Connect("tcp://localhost:5563")
	subscriber.Bind(fmt.Sprintf("tcp://*:%s", getEnv("RCV", "5566")))
	subscriber.SetSubscribe("/loadbalancer")
	fmt.Printf("Listening for messages, on ZMQ.\n")

	for {
		address, _ := subscriber.Recv(0)
		content, _ := subscriber.Recv(0)
		request := ApplicationRequest{}

		err := json.Unmarshal([]byte(content), &request)
		if err != nil {
			fmt.Println("Error: ", err)
		}

		application := strings.Split(address, "/")[2]

		if strings.HasSuffix(address, "join") {
			if p.HasApplication(application) {
				p.Join(application, request.Target)
			} else {
				p.CreateApplication(request)
			}
		}

		if strings.HasSuffix(address, "leave") {
			if p.HasApplication(application) {
				p.Leave(application, request.Target)
			}
		}

		// Update healthcheck
		p.SetHealthcheck(strings.Split(address, "/")[2], request.Healthcheck)

		// Update routes
		p.FindAndUpdateRoutes(strings.Split(address, "/")[2], request.Routes)

		// Remove Empty applications.
		p.RemoveEmptyApplication()

		fmt.Printf("Done treating [%s], %s.\n", address, request)
	}
}

func (p *Application) PerformHealthchecks() {
	p.check()
	if p.IsEnd() == false {
		p.Next.PerformHealthchecks()
	}
}

func main() {

	rand.Seed(time.Now().Unix()) // initialize global pseudo random generator

	// We set, this to -1, thus default is 0.
	hello := Application{Application: "root", Root: true, Order: 999999, Strategy: "RoundRobinStrategy"}

	go hello.subscribe()

	// Run healthchecks.
	ticker := time.NewTicker(5000 * time.Millisecond)
	go func() {
		for t := range ticker.C {
			fmt.Printf("Running healthchecks at %s.\n", t)
			hello.PerformHealthchecks()
		}
	}()

	// Run stats.
	go hello.PerformStatsAssembly("30s")

	// Start webserver.
	err := http.ListenAndServe(fmt.Sprintf(":%s", getEnv("PORT", "9999")), &hello)
	log.Fatal(err)
}
