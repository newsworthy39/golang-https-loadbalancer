package main

import (
	"bytes"
	"errors"
	"sync"
	//	"encoding/json"
	"fmt"
	//	zmq "github.com/pebbe/zmq4"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	//	"regexp"
	"flag"
	"github.com/BenLubar/memoize"
	"net"
	"net/url"
	"strings"
	"time"
)

// externalIP
// Returns externalIP if possible, or err.
// Memoize this function, to prevent excessive iterating.
func externalIP() (string, error) {
	var _externalIP = func() (string, error) {
		ifaces, err := net.Interfaces()
		if err != nil {
			return "", err
		}
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue // interface down
			}
			if iface.Flags&net.FlagLoopback != 0 {
				continue // loopback interface
			}
			addrs, err := iface.Addrs()
			if err != nil {
				return "", err
			}
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() {
					continue
				}
				ip = ip.To4()
				if ip == nil {
					continue // not an ipv4 address
				}
				return ip.String(), nil
			}
		}
		return "", errors.New("are you connected to the network?")
	}
	_externalIP = memoize.Memoize(_externalIP).(func() (string, error))
	return _externalIP()
}

type bufferedResponseWriter struct {
	http.ResponseWriter // embed struct
	HTTPStatus          int
	ResponseSize        int
	buf                 *bytes.Buffer
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

type ProxyTargetRule struct {
	Target                   string
	transport                [64]*http.Transport
	MaxBackendConnections    int
	activeBackendConnections int
	Next                     *http.Handler
}

func NewProxyTargetRule(Destination string, MaxBackends int) *ProxyTargetRule {
	return &ProxyTargetRule{Target: Destination,
		MaxBackendConnections: MaxBackends}
}

func (p *ProxyTargetRule) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	if p.transport[p.activeBackendConnections] == nil {
		p.transport[p.activeBackendConnections] = &http.Transport{
			MaxIdleConns:       10,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: false}
	}

	// Setup client, to *not* follow redirects, thanks to this hack.
	client := &http.Client{
		Transport: p.transport[p.activeBackendConnections],
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// p.Target = http://something:port/maybethis
	org, er := url.Parse(p.Target)
	if er != nil {
		fmt.Printf("Error parsing Target in ProxyTargetRule, %s, err: %s", p.Target, er)
	}

	// We allways, append our own "IP" or DNS-name. This function is mmeoized internally.
	host, _ := externalIP()

	breq, err := http.NewRequest("GET", fmt.Sprintf("%s://%s%s", org.Scheme, org.Host, req.URL.Path), nil)
	breq.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
	breq.Header.Set("X-Forwarded-For", fmt.Sprintf("%s, %s", req.Header.Get("X-Forwarded-For"), host))
	breq.Header.Set("X-Forwarded-Proto", req.URL.Scheme)

	resp, err := client.Do(breq)

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

	if p.Next != nil {
		(*p.Next).ServeHTTP(res, req)
	}
}

type ContentTargetRule struct {
	Content    string
	header     http.Header
	StatusCode int
	Next       *http.Handler
}

func (c *ContentTargetRule) Header() http.Header {
	return c.header
}

func NewContentCompleteTargetRule(Content string, Headers []string, StatusCode int) *ContentTargetRule {
	t := &ContentTargetRule{Content: Content,
		header:     make(map[string][]string),
		StatusCode: StatusCode}

	for _, element := range Headers {
		s := strings.SplitN(element, ":", 2)
		t.Header().Add(s[0], s[1])
	}
	return t
}

func NewContentTargetRule(Content string) *ContentTargetRule {
	t := &ContentTargetRule{Content: Content,
		header:     make(map[string][]string),
		StatusCode: 200}
	return t
}

func NewRedirectTargetRule(Destination string, Redirect int) *ContentTargetRule {
	t := &ContentTargetRule{Content: fmt.Sprintf("Content Moved HTTP %d", Redirect),
		header:     make(map[string][]string),
		StatusCode: Redirect}
	t.Header().Add("Location", Destination)

	return t
}

func (p *ContentTargetRule) ServeHTTP(res http.ResponseWriter, req *http.Request) {

	// Copy headers (http.headers support) - Migrate, to this.
	for k, v := range p.Header() {
		res.Header()[k] = v
	}

	res.WriteHeader(p.StatusCode)
	res.Write([]byte(p.Content))
}

// PropositionTargets allow branching.
type PropositionTargetRule struct {
	Left *http.Handler
	Right *http.Handler
}

type CacheTargetRule struct {
	Cache *bufferedResponseWriter
	IsNew bool
	sync.RWMutex
	Next *http.Handler
}

func NewCacheTargetRule(Destination string) *CacheTargetRule {
	target := NewProxyTargetRule(Destination, 10)
	rule := CacheTargetRule{IsNew: true}
	rule.AddTargetRule(target)
	return &rule
}

func (c *CacheTargetRule) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	// We have multiple critical regions, every access to shared resource is
	// rlock'ed or lock'ed.
	// Setup read-locking, using double-locking.
	c.RLock()
	if c.IsNew {
		c.RUnlock()
		c.Lock()
		interceptWriter := &bufferedResponseWriter{res, 0, 0, bytes.NewBuffer(nil)}
		(*c.Next).ServeHTTP(interceptWriter, req)

		interceptWriter.Header().Add("X-Cache-Hit", "HIT")
		c.Cache = interceptWriter
		c.IsNew = false
		c.Unlock()
	}

	// Read-lock for copying.
	c.RLock()
	for name, values := range c.Cache.Header() {
		res.Header()[name] = values
	}
	res.WriteHeader(c.Cache.HTTPStatus)
	res.Write(c.Cache.buf.Bytes())
	c.RUnlock()
}

type RouteExpression struct {
	Path        string
	StatusCodes []int64
	AccessTime  int64
	Next        *http.Handler
}

func (r *RouteExpression) AddTargetRule(rule http.Handler) {
	r.Next = &rule
}

func (t *ContentTargetRule) AddTargetRule(rule http.Handler) {
	t.Next = &rule
}
func (t *ProxyTargetRule) AddTargetRule(rule http.Handler) {
	t.Next = &rule
}

func (t *CacheTargetRule) AddTargetRule(rule http.Handler) {
	t.Next = &rule
}

func NewRouteExpression(Path string) *RouteExpression {
	route := new(RouteExpression)
	route.Path = Path
	return route
}

func (r *RouteExpression) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	(*r.Next).ServeHTTP(res, req)
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

func (l *List) FindTargetGroupByRouteExpression(req *http.Request) (RouteExpression, error) {
	list := l.head
	for list != nil {
		routeExpression := list.key.(RouteExpression)
		// http(s)://somedomain.com:$PORT/Path == http(s)://somedomain.com:$PORT/Path
		if strings.HasPrefix(fmt.Sprintf("%s://%s%s", req.URL.Scheme, req.Host, req.URL.Path),
			routeExpression.Path) {
			return routeExpression, nil
		}
		list = list.next
	}
	return RouteExpression{}, errors.New("FindTargetGRoupByRouteExpression: No routes found")
}

// NCSA Logging Format to log.
func NCSALogger(next http.HandlerFunc, logToStdout bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		if logToStdout {
			t := time.Now()
			interceptWriter := bufferedResponseWriter{w, 0, 0, bytes.NewBuffer(nil)}

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
		} else {
			next.ServeHTTP(w, r)
		}
	}
}

// This ensures, we have the necessary http-headers, to look in our lists.
func EnsureHTTPProtocolHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Make sure, we have a protocol, matching our listener proto.
		r.URL.Scheme = "http"
		next.ServeHTTP(w, r)
	}
}

func main() {

	host, err := externalIP()
	if err != nil {
		fmt.Println(err)
	}

	// we will need some args, going here.
	logToStdout := flag.Bool("log", false, "Log to stdout.")
	port := flag.Int("port", 9000, "Port to listen to.")
	apiBackend := flag.String("apiBackend", "10.90.10.80", "Which backends to use for API-access.")
	apiDomain := flag.String("apiDomain", "clouddom.eu", "What apex-domain is used for infrastructure.")

	flag.Parse()

	fmt.Printf("Ports bound %s:%d, apiDomain: %s, apiBackend: %s \n", host, *port, *apiBackend, *apiDomain)

	// Create root-node in graph.
	routeexpressions := new(List)

	/* Functionality testing */
	route := NewRouteExpression(fmt.Sprintf("http://%s:%d/redirect", host, *port))
	rootRule := NewRedirectTargetRule("https://www.dr.dk/", 301)
	route.AddTargetRule(rootRule)
	routeexpressions.Insert(*route)

	/* Functionality testing */
	cacheRoute := NewRouteExpression(fmt.Sprintf("http://%s:%d/cache", host, *port))
	// TODO: Offer af Mix-In, to NewCacheTargetRule,
	// using different cache-backends, either
	// memcache-backends, http-slave etc
	backendCacheRule := NewCacheTargetRule("http://www.tuxand.me")
	cacheRoute.AddTargetRule(backendCacheRule)
	routeexpressions.Insert(*cacheRoute)

	/* Functionality testing */
	contentRoute := NewRouteExpression(fmt.Sprintf("http://%s:%d/content", host, *port))
	contentCacheRule := NewContentTargetRule("http://www.microscopy-uk.org.uk/mag/indexmag.html")
	contentRoute.AddTargetRule(contentCacheRule)
	routeexpressions.Insert(*contentRoute)

	/* Functionality testing */
	proxyRoute := NewRouteExpression(fmt.Sprintf("http://%s:%d/api", host, *port))
	proxyRule := NewProxyTargetRule("https://www.tuxand.me", 10)
	proxyRoute.AddTargetRule(proxyRule)
	routeexpressions.Insert(*proxyRoute)

	// Start api-part. We have hard-boiled api-hostnames in here, to 
	// match our own infrastructure. That is, requests going to apiDomain
	// are sent to those systems.
	apiRoute := NewRouteExpression(fmt.Sprintf("http://api.%s", *apiDomain))
	apiProxyRoute := NewProxyTargetRule(fmt.Sprintf("http://%s", *apiBackend), 10)
	apiRoute.AddTargetRule(apiProxyRoute)
	routeexpressions.Insert(*apiRoute)

	// Start webserver, capture apps and use that.
	http.HandleFunc("/", EnsureHTTPProtocolHeaders(
		NCSALogger(
			func(res http.ResponseWriter, req *http.Request) {

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
			}, *logToStdout)))

	err = http.ListenAndServe(fmt.Sprintf("%s:%d", host, *port), nil)
	log.Fatal(err)
}
