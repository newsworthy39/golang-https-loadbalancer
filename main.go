package main

import (
	"errors"
//	"encoding/json"
	"fmt"
//	zmq "github.com/pebbe/zmq4"
	"io/ioutil"
	"log"
	"net/http"
	"os"
//	"regexp"
	"strings"
	"time"
	uuid "github.com/google/uuid"
)

type stResponseWriter struct {
	http.ResponseWriter
	HTTPStatus   int
	ResponseSize int
}

func (w *stResponseWriter) WriteHeader(status int) {
	w.HTTPStatus = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *stResponseWriter) Flush() {
	z := w.ResponseWriter
	if f, ok := z.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *stResponseWriter) CloseNotify() <-chan bool {
	z := w.ResponseWriter
	return z.(http.CloseNotifier).CloseNotify()
}

func (w *stResponseWriter) Write(b []byte) (int, error) {
	if w.HTTPStatus == 0 {
		w.HTTPStatus = 200
	}
	w.ResponseSize = len(b)
	return w.ResponseWriter.Write(b)
}

type Target interface {
	ServeHTTP( res http.ResponseWriter, req *http.Request) bool
}

type ProxyTargetRule struct {
	Target string
	transport [64]*http.Transport
	MaxBackendConnections int
	activeBackendConnections int
}

type CacheTargetRule struct {
	Content string
	Headers []string
	StatusCode int
}

func (p* ProxyTargetRule) ServeHTTP( res http.ResponseWriter, req *http.Request) bool {
	if p.transport[p.activeBackendConnections] == nil {
		p.transport[p.activeBackendConnections] = &http.Transport{
			MaxIdleConns:       10,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: true }
	}

	client := &http.Client{Transport: p.transport[p.activeBackendConnections] }
	resp, err := client.Get(p.Target)

	if err != nil {
		// handle error
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	res.WriteHeader(resp.StatusCode)
	res.Write([]byte(body))

	p.activeBackendConnections++

	if p.activeBackendConnections%p.MaxBackendConnections == 0 {
		p.activeBackendConnections = 0
	}

	return true
}


func (p* CacheTargetRule) ServeHTTP( res http.ResponseWriter, req *http.Request) bool {
	for _, element := range p.Headers {
		s := strings.SplitN(element,":", 2)
		res.Header().Add(s[0], s[1])
	}
	res.WriteHeader(p.StatusCode)
	res.Write([]byte(p.Content))

	return true
}

type RouteExpression struct {
	Uuid uuid.UUID
	Path string
	Host string
        StatusCodes []int64
        AccessTime  int64
	Target []Target
}

func (r* RouteExpression) ServeHTTP( res http.ResponseWriter, req *http.Request) {
	// We loop through all elements, until we hit somebody, 
	// taking responsibility, for execution. That is, 
	// we use early-return, to abort filter-execution.
	for _, element := range r.Target {
		if (element.ServeHTTP(res, req)) {
			break ;
		}
	}
}

func (r* RouteExpression) AddTargetRule(rule Target) {
	r.Target = append(r.Target, rule)
}

func NewRouteExpression(Path string, Host string ) *RouteExpression {
	route := new(RouteExpression)
	route.Uuid,_ = uuid.NewRandom()
	route.Path = Path
	route.Host = Host
	return route
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
		// if / == / and http(s)://somedomain.com:$PORT == http(s)://somedomain.com:$PORT

		if (strings.HasSuffix(req.URL.Path, routeExpression.Path) &&
			strings.Compare(fmt.Sprintf("%s://%s", req.URL.Scheme, req.Host),
				routeExpression.Host) == 0) {

			return routeExpression, nil
		}
                list = list.next
        }
	return RouteExpression{}, errors.New("FindTargetGRoupByRouteExpression: No routes found")
}

func NCSALogger(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		t := time.Now()
		interceptWriter := stResponseWriter{w, 0, 0}

		next.ServeHTTP(&interceptWriter, r)

		log.Printf("%s - %s - - %s \"%s %s %s\" %d %d %s %dus\n",
			r.URL.Scheme,
			r.RemoteAddr,
			t.Format("02/Jan/2006:15:04:05 -0700"),
			r.Method,
			r.URL.Path,
			r.Proto,
			interceptWriter.HTTPStatus,
			interceptWriter.ResponseSize,
			r.UserAgent(),
			time.Since(t),
		)
	}
}

func EnsureHTTPProtocolHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Make sure, we have a protocol, matching our listener proto.
		r.URL.Scheme = "http"

		next.ServeHTTP(w, r)
	}
}

func main() {

	// We set, this to -1, thus default is 0.
	routeexpressions := new(List)

	route := NewRouteExpression("/", fmt.Sprintf("http://192.168.1.11:%s", getEnv("PORT", "9999")))
	route.AddTargetRule(&CacheTargetRule{ Content: "Location moved", StatusCode: 301, Headers: []string { "Location: https://www.dr.dk"} } )
	route.AddTargetRule(&ProxyTargetRule{ Target: "https://www.tuxand.me", MaxBackendConnections: 4})
	routeexpressions.Insert (*route)

	// Start webserver, capture apps and use that.
	http.HandleFunc("/", EnsureHTTPProtocolHeaders(
				NCSALogger(
					func( res http.ResponseWriter, req *http.Request) {

		// Make sure, we have a protocol, matching our listener proto.
		req.URL.Scheme = "http"

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
		routeExpression.ServeHTTP(res, req)

		return
	})))

	err := http.ListenAndServe(fmt.Sprintf(":%s", getEnv("PORT", "9999")), nil)
	log.Fatal(err)
}
