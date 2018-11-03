package main

import (
	"errors"
//	"encoding/json"
	"fmt"
//	zmq "github.com/pebbe/zmq4"
//	"io/ioutil"
	"log"
	"net/http"
	"os"
//	"regexp"
//	"strings"
	uuid "github.com/google/uuid"
)

type TargetGroup struct {
	Uuid uuid.UUID
}

type RouteExpression struct {
	Uuid uuid.UUID
	Path string
	Host string
        StatusCodes []int64
        AccessTime  int64
	TargetGroupUUID int64
}

type Node struct {
	prev *Node
	next *Node
	key  interface{}
}

type List struct {
	head *Node
	tail *Node
}

func (L *List) Insert(key interface{}) {
	list := &Node{
		next: L.head,
		key:  key,
	}
	if L.head != nil {
		L.head.prev = list
	}
	L.head = list

	l := L.head
	for l.next != nil {
		l = l.next
	}
	L.tail = l
}

func (l *List) Display() {
	list := l.head
	for list != nil {
		fmt.Printf("%+v ->", list.key)
		list = list.next
	}
	fmt.Println()
}

func Display(list *Node) {
	for list != nil {
		fmt.Printf("%+v ->", list.key)
		list = list.next
	}
	fmt.Println()
}

func ShowBackwards(list *Node) {
	for list != nil {
		fmt.Printf("%v <-", list.key)
		list = list.prev
	}
	fmt.Println()
}

func (l *List) Reverse() {
	curr := l.head
	var prev *Node
	l.tail = l.head

	for curr != nil {
		next := curr.next
		curr.next = prev
		prev = curr
		curr = next
	}
	l.head = prev
	Display(l.head)
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


func (l* List) FindTargetGroupByRouteExpression(req *http.Request) (RouteExpression, error) {
        list := l.head
        for list != nil {
		routeExpression := list.key.(RouteExpression)
		if (req.URL.Path == routeExpression.Path) {
			return routeExpression, nil
		}
                fmt.Printf("%+v ->", list.key)
                list = list.next
        }
        fmt.Println()
	return RouteExpression{}, errors.New("FindTargetGRoupByRouteExpression: No routes found")
}


func main() {

	// We set, this to -1, thus default is 0.
	routeexpressions := new(List)

	// Start webserver, capture apps and use that.
	http.HandleFunc("/", func( res http.ResponseWriter, req *http.Request) {
		// Next, run though apps, and find a exact-match.
		routeExpression, err := routeexpressions.FindTargetGroupByRouteExpression(req)
		if err != nil {
			// Deliver, not found, here is a problem to do sort-of-a-root-accounting.
			res.WriteHeader(http.StatusNotFound)
			res.Write([]byte(fmt.Sprintf("Not found (%s!).", req.URL.Path[1:])))
			// Error to flush the io-streams
			// http.Error(res,
			//	fmt.Sprintf("This loadbalancer has not yet been configured."),
			//	http.StatusInternalServerError)
			return

		}

		// pseudo-code
		// RouteExpression would link to a target-group. Send a go-func, there.
		// Target-groups can be a number of things.
		// * Proxies to backend-ssystems
		// * Redirect-applications (ie, http->https redirects, )
		// * Publisher-producer system (ie, uploaded error-pages etc)
		// * Proxy-cache event-based notification systems
		go func( res http.ResponseWriter, req *http.Request, rs RouteExpression) {
			fmt.Printf("Serving %+v", rs)
		}(res, req, routeExpression)

	})

	err := http.ListenAndServe(fmt.Sprintf(":%s", getEnv("PORT", "9999")), nil)
	log.Fatal(err)
}
