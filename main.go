package main

import (
	"errors"
	"sync"
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
	"github.com/newsworthy39/golang-https-loadbalancer/util"
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
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	w.HTTPStatus = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *bufferedResponseWriter) Flush() {
	z := w.ResponseWriter
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
		w.HTTPStatus = http.StatusOK
		w.WriteHeader(http.StatusOK)
	}
	w.ResponseSize = w.ResponseSize + len(b)
	return w.ResponseWriter.Write(b)
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

	breq, err := http.NewRequest(req.Method, fmt.Sprintf("%s://%s%s", org.Scheme, org.Host, req.URL.Path), nil)
	breq.Header.Set("X-Forwarded-Host", req.Host)
	breq.Header.Set("X-Forwarded-For", fmt.Sprintf("%s, %s", req.Header.Get("X-Forwarded-For"), req.RemoteAddr))
	breq.Header.Set("X-Forwarded-Proto", req.URL.Scheme)
	breq.Header.Set("User-Agent", req.Header.Get("User-Agent"))

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
	Left  *http.Handler
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
		interceptWriter := &bufferedResponseWriter{res, 0, 0}
		defer interceptWriter.Flush()
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
	//res.Write(c.Cache.buf.Bytes())
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

//
// Helpers
//
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// NCSA Logging Format to log.
func NCSALogger(next http.HandlerFunc, logToStdout bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		if logToStdout {
			t := time.Now()

			interceptWriter := bufferedResponseWriter{w, 0, 0}

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

			defer interceptWriter.Flush()

		} else {
			next.ServeHTTP(w, r)
		}
	}
}

// This ensures, we have the necessary http-headers, to look in our lists.
func EnsureProtocolHeaders(next http.HandlerFunc, Headers []string, Scheme string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Make sure, we have a protocol, matching our listener proto.

		for _, element := range Headers {
			s := strings.SplitN(element, ":", 2)
			w.Header().Set(s[0], s[1])
		}

		r.URL.Scheme = Scheme
		next.ServeHTTP(w, r)
	}
}

func FindTargetGroupByRouteExpression(routeexpressions *util.List, req *http.Request) (*RouteExpression, error) {

	rs, err := routeexpressions.Find(func(key *interface{}) bool {
		routeExpression := (*key).(RouteExpression)
		if strings.HasPrefix(fmt.Sprintf("%s://%s%s", req.URL.Scheme,
				req.Host, req.URL.Path), routeExpression.Path) {
			return true
		}
		return false
	})

	if (err != nil) {
		return &RouteExpression{}, err
	}

	rs1 := (*rs).(RouteExpression)
	return &rs1, err

}


func main() {

	host, err := externalIP()
	if err != nil {
		fmt.Println(err)
	}

	// we will need some args, going here.
	logToStdout := flag.Bool("log", false, "Log to stdout.")
	listen := flag.String("listen", fmt.Sprintf("%s:%d", host, 8080), "Listen description.")
	apiBackend := flag.String("apiBackend", "http://10.90.10.80", "Which backends to use for API-access.")
	apiDomain := flag.String("apiDomain", "clouddom.eu", "What apex-domain is used for infrastructure.")
	flag.Parse()

	// Output some sensible information about operation.
	fmt.Printf("Listen :%s, apiDomain: %s, apiBackend: %s \n", *listen, *apiBackend, *apiDomain)

	// Create root-node in graph, and monkey-patch our configuration onto it.
	routeexpressions := util.LoadConfiguration(*apiBackend, *apiDomain)

	// Start api-part. We have hard-boiled api-hostnames in here, to
	// match our own infrastructure. That is, requests going to apiDomain
	// are sent to those systems. We extract the return-code, to know
	// if we're supposed to check something ourselves.
	apiRoute := NewRouteExpression(fmt.Sprintf("http://api.%s", *apiDomain))
	apiProxyIntercept := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			interceptWriter := bufferedResponseWriter{w, 0, 0}
			defer interceptWriter.Flush()

			if len(r.URL.Query().Get("key")) == 0 {
				http.Error(w, "missing key", http.StatusUnauthorized)
				return
			}

			h.ServeHTTP(&interceptWriter, r)

			if r.Method != http.MethodGet && interceptWriter.HTTPStatus == http.StatusOK {
				log.Printf("Scheduling refresh, because of api-change. (%d)\n",
					interceptWriter.HTTPStatus)
			}
		})
	}
	apiProxyRoute := NewProxyTargetRule(*apiBackend, 10)
	apiRoute.AddTargetRule(apiProxyIntercept(apiProxyRoute))
	routeexpressions.Insert(*apiRoute)

	// Start webserver, capture apps and use that.
	http.HandleFunc("/",
		NCSALogger(
			EnsureProtocolHeaders(
				func(res http.ResponseWriter, req *http.Request) {

					// Next, run though apps, and find a exact-match.

					rs, err := FindTargetGroupByRouteExpression(routeexpressions, req)
					if err != nil {
						// Deliver, not found, here is a problem to do sort-of-a-root-accounting.
						res.WriteHeader(http.StatusNotFound)
						res.Write([]byte(fmt.Sprintf("Not found (%s!).", req.URL.Path[1:])))
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

				}, []string{"X-Loadbalancer: Golang-Accelerator"}, "http"), *logToStdout))

	err = http.ListenAndServe(*listen, nil)
	log.Fatal(err)
}
