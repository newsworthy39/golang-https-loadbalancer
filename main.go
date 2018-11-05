package main

import (
	"sync"
	"bytes"
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

type bufferedResponseWriter struct {
    http.ResponseWriter // embed struct
    HTTPStatus   int
    ResponseSize int
    buf *bytes.Buffer
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	w.HTTPStatus = status
}

func (w *bufferedResponseWriter) Flush() {
	z := w.ResponseWriter
	z.WriteHeader(w.HTTPStatus)
	z.Write(w.buf.Bytes())
	if f, ok := z.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *bufferedResponseWriter) CloseNotify() <-chan bool {
	z := w.ResponseWriter
	return z.(http.CloseNotifier).CloseNotify()
}

func (w *bufferedResponseWriter) Write(b []byte) (int, error) {
	if w.HTTPStatus == 0 {
		w.HTTPStatus = 200
	}
	w.ResponseSize = len(b)
        return w.buf.Write(b)
}

type Target interface {
	ServeHTTP( res http.ResponseWriter, req *http.Request)
	AddTargetRule(Target Target)
}

type ProxyTargetRule struct {
	Target string
	transport [64]*http.Transport
	MaxBackendConnections int
	activeBackendConnections int
	Next* Target
}

func NewProxyTargetRule(Destination string, MaxBackends int) *ProxyTargetRule {
	return &ProxyTargetRule{Target: Destination,
		MaxBackendConnections : MaxBackends,}
}

func (p* ProxyTargetRule) ServeHTTP( res http.ResponseWriter, req *http.Request)  {
	if p.transport[p.activeBackendConnections] == nil {
		p.transport[p.activeBackendConnections] = &http.Transport{
			MaxIdleConns:       10,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: true }
	}

	// Setup client.
	client := &http.Client{
		Transport: p.transport[p.activeBackendConnections],
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(p.Target)
	if err != nil {
		// handle error
	}
	defer resp.Body.Close()

	for name, values := range resp.Header {
                res.Header()[name] = values
        }

	body, err := ioutil.ReadAll(resp.Body)
	res.WriteHeader(resp.StatusCode)
	res.Write([]byte(body))

	// Increase and rotate mod MaxBackendConnections.
	p.activeBackendConnections++
	if p.activeBackendConnections%p.MaxBackendConnections == 0 {
		p.activeBackendConnections = 0
	}

	if (p.Next != nil) {
		(*p.Next).ServeHTTP(res, req)
	}
}

type ContentTargetRule struct {
	Content string
	header http.Header
	StatusCode int
	Next* Target
}

func (c* ContentTargetRule) Header() http.Header {
	return c.header
}

func NewContentCompleteTargetRule(Content string, Headers []string, StatusCode int) *ContentTargetRule {
	t := &ContentTargetRule{Content: Content,
		header : make(map[string][]string),
		StatusCode: StatusCode}

	for _, element := range Headers {
		s := strings.SplitN(element,":", 2)
		t.Header().Add(s[0], s[1])
	}

	t.Header().Add("X-CacheRule","TRUE")
	return t
}


func NewContentTargetRule(Content string) *ContentTargetRule {
	t := &ContentTargetRule{Content: Content,
		header : make(map[string][]string),
		StatusCode: 200}

	t.Header().Add("X-CacheRule","TRUE")

	return t
}

func NewRedirectTargetRule(Destination string, Redirect int) *ContentTargetRule {
	t := &ContentTargetRule{Content: fmt.Sprintf("Content Moved HTTP %d", Redirect),
		header : make(map[string][]string),
		StatusCode: Redirect }

	t.Header().Add("X-CacheRule","TRUE")
	t.Header().Add("Location", Destination)

	return t
}

func (p* ContentTargetRule) ServeHTTP( res http.ResponseWriter, req *http.Request)  {

	// Copy headers (http.headers support) - Migrate, to this.
	for k, v := range p.Header() {
		res.Header()[k] = v
	}

	res.WriteHeader(p.StatusCode)
	res.Write([]byte(p.Content))
}

type CacheTargetRule struct {
	Cache *bufferedResponseWriter
	IsNew bool
	sync.RWMutex
	Next *Target
}

func NewCacheTargetRule(Destination string) *CacheTargetRule {
	target := NewProxyTargetRule(Destination, 10)
	rule   := CacheTargetRule{ IsNew: true }
	rule.AddTargetRule(target)
	return &rule
}

func (c* CacheTargetRule) ServeHTTP ( res http.ResponseWriter, req *http.Request) {

	// Setup read-locking, using double-locking.
	c.RLock()
	if c.IsNew {
	c.RUnlock()
			c.Lock()
			interceptWriter := &bufferedResponseWriter{res, 0, 0, bytes.NewBuffer(nil) }
			(*c.Next).ServeHTTP(interceptWriter, req)

			interceptWriter.Header().Add("X-Cache-Hit", "HIT")
			c.Cache = interceptWriter
			c.IsNew = false
			c.Unlock()
	}

	c.RLock()
	for name, values := range c.Cache.Header() {
		res.Header()[name] = values
	}
	res.WriteHeader(c.Cache.HTTPStatus)
	res.Write(c.Cache.buf.Bytes())
	c.RUnlock()
}

type RouteExpression struct {
	Uuid uuid.UUID
	Path string
	Host string
        StatusCodes []int64
        AccessTime  int64
	Next *Target
}

func (r* RouteExpression) AddTargetRule(rule Target) {
	r.Next = &rule
}

func (t* ContentTargetRule) AddTargetRule(rule Target) {
	t.Next = &rule
}
func (t* ProxyTargetRule) AddTargetRule(rule Target) {
	t.Next = &rule
}

func (t* CacheTargetRule) AddTargetRule(rule Target) {
	t.Next = &rule
}


func NewRouteExpression(Path string, Host string ) *RouteExpression {
	route := new(RouteExpression)
	route.Uuid,_ = uuid.NewRandom()
	route.Path = Path
	route.Host = Host
	return route
}

func (r* RouteExpression) ServeHTTP( res http.ResponseWriter, req *http.Request) {
	(*r.Next).ServeHTTP(res,req)
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

// NCSA Logging Format to log.
func NCSALogger(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		t := time.Now()
		interceptWriter := bufferedResponseWriter{w, 0, 0, bytes.NewBuffer(nil) }

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

		// BufferedResponseWriters, require us to manually, call Flush, 
		// This emits, the recorded status and body.
		interceptWriter.Flush()
	}
}

// This ensures, we have the necessary http-headers, to look in our lists.
func EnsureHTTPProtocolHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Make sure, we have a protocol, matching our listener proto.
		r.URL.Scheme = "http"
		r.Proto = "http"

		next.ServeHTTP(w, r)
	}
}

func main() {

	// We set, this to -1, thus default is 0.
	routeexpressions := new(List)

	/* Functionality testing */
	route := NewRouteExpression("/", fmt.Sprintf("http://192.168.1.11:%s", getEnv("PORT", "9999")))

	rootRule := NewRedirectTargetRule( "https://www.dr.dk/", 301 )
	rootRule.AddTargetRule(NewProxyTargetRule("https://www.tuxand.me", 4)) // this is meaningless.
	route.AddTargetRule(rootRule)

	routeexpressions.Insert (*route)


	/* Functionality testing */
	cacheRoute := NewRouteExpression("/cache", fmt.Sprintf("http://%s:%s", getEnv("HOST", "localhost"), getEnv("PORT", "9999")))
	backendCacheRule := NewCacheTargetRule( "http://www.tuxand.me" )
	cacheRoute.AddTargetRule(backendCacheRule )
	routeexpressions.Insert (*cacheRoute)

	/* Functionality testing */
	contentRoute := NewRouteExpression("/content", fmt.Sprintf("http://%s:%s", getEnv("HOST", "localhost"), getEnv("PORT", "9999")))
	contentCacheRule := NewContentTargetRule( "http://www.microscopy-uk.org.uk/mag/indexmag.html" )
	contentRoute.AddTargetRule(contentCacheRule)
	routeexpressions.Insert (*contentRoute)

	// Start webserver, capture apps and use that.
	http.HandleFunc("/", EnsureHTTPProtocolHeaders(
//				NCSALogger(
					func( res http.ResponseWriter, req *http.Request) {

		// Make sure, we have a protocol, matching our listener proto.
		req.URL.Scheme = "http"

		// Next, run though apps, and find a exact-match.
		rs, err := routeexpressions.FindTargetGroupByRouteExpression(req)
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
		rs.ServeHTTP(res, req)

		return
	}))
//)

	err := http.ListenAndServe(fmt.Sprintf(":%s", getEnv("PORT", "9999")), nil)
	log.Fatal(err)
}
