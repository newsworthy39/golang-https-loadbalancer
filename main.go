package main

import (
	"errors"
	"fmt"
	"sync"
	//	zmq "github.com/pebbe/zmq4"
	"io/ioutil"
	"log"
	"net/http"
	//	"regexp"
	"flag"
	"github.com/BenLubar/memoize"
	"github.com/newsworthy39/golang-https-loadbalancer/util"
	"net"
	"net/url"
	"strings"
	"time"
	"math/rand"
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

func NewProxyTargetRule(Destination util.Backend, MaxBackends int) *ProxyTargetRule {
	return &ProxyTargetRule{Target: Destination.Backend,
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

	breq, err := http.NewRequest(req.Method, fmt.Sprintf("%s://%s%s", org.Scheme, org.Host, req.URL.Path),
			req.Body)

	breq.Header.Set("X-Forwarded-Host", req.Host)
	breq.Header.Set("X-Forwarded-For", fmt.Sprintf("%s, %s", req.Header.Get("X-Forwarded-For"), req.RemoteAddr))
	breq.Header.Set("X-Forwarded-Proto", req.URL.Scheme)
	breq.Header.Set("User-Agent", req.Header.Get("User-Agent"))

	if  element := req.Header.Get("AccessKey"); element != "" {
		breq.Header.Set("AccessKey", element)
	}

	if  secret := req.Header.Get("Secret"); secret != "" {
		breq.Header.Set("Secret", secret)
	}


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

func (t *ProxyTargetRule) AddTargetRule(rule http.Handler) {
	t.Next = &rule
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

func (t *ContentTargetRule) AddTargetRule(rule http.Handler) {
	t.Next = &rule
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

func NewCacheTargetRule(Destination util.Backend) *CacheTargetRule {
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
func (t *CacheTargetRule) AddTargetRule(rule http.Handler) {
	t.Next = &rule
}

type RouteExpression struct {
	Path        string
	Next        *http.Handler
}

func NewRouteExpression(Path string) *RouteExpression {
	route := new(RouteExpression)
	route.Path = Path
	return route
}

func (r *RouteExpression) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	(*r.Next).ServeHTTP(res, req)
}

func (r *RouteExpression) AddTargetRule(rule http.Handler) {
	r.Next = &rule
}

func RandomStrategy(lb *LoadBalancer) int {
        return rand.Intn(lb.Count)
}

func RoundRobinStrategy(lb *LoadBalancer) int {
        r := lb.Requests
	if (lb.Count > 0) {
	        return int(r) % lb.Count
	} else {
		return 0
	}
}

func SelectStrategy(lb *LoadBalancer) int {
        m := map[string]func(lb *LoadBalancer) int {
                "round-robin": RoundRobinStrategy,
                "random": RandomStrategy,
        }
	_, prs := m[lb.Method]
	if prs == false {
	        return m["round-robin"](lb)
	} else {
		return m[lb.Method](lb)
	}
}


type LoadBalancer struct {
	Requests     int
	Next         [64]*http.Handler
	Count	     int
	Method       string
}

func NewLoadBalancer(method string) *LoadBalancer {
	return &LoadBalancer { Count: 0, Requests: 0, Method: method}
}

func (l *LoadBalancer) AddTargetRule(rule http.Handler) {
	l.Next[l.Count] = &rule
	l.Count++
}

func (l *LoadBalancer) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	l.Requests++
	if l.Count != 0 {
		candidate := SelectStrategy(l)
		(*(l.Next[candidate])).ServeHTTP(res, req)
	}
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

	if err != nil {
		return &RouteExpression{}, err
	}

	rs1 := (*rs).(RouteExpression)
	return &rs1, err

}

func LoadConfiguration(apiConfig *util.ApiConfiguration, root *util.List) (error) {
	routes := apiConfig.LoadConfigurationFromRESTApi()

	for _, route := range routes {

		fmt.Printf("%+v", route);

		// {Type:ProxyTarget Path:http://test.api.comf/api Loadbalancing:round-robin
		// Backends:[https://www.tuxand.me]}
		if route.Type == "ProxyTarget" {
			rootRoute := NewRouteExpression(route.Path)
			lb := NewLoadBalancer(route.Method)
			for _, backend := range route.Backends {
				lb.AddTargetRule(NewProxyTargetRule(backend, 10))
			}

			rootRoute.AddTargetRule(lb)
			root.Insert(*rootRoute)
		}
	}

	return nil
}

func main() {

	host, err := externalIP()
	if err != nil {
		fmt.Println(err)
		host = "*"
	}

	// we will need some args, going here.
	logToStdout := flag.Bool("log", false, "Log to stdout.")
	listen := flag.String("listen", fmt.Sprintf("%s:%d", host, 443), "Listen description.")
	apiBackend := flag.String("apiBackend", "http://10.90.10.80", "Which backends to use for API-access.")
	apiDomain := flag.String("apiDomain", "clouddom.eu", "What apex-domain is used for infrastructure.")
	secret := flag.String("secret", "", "The secret associated.")
	access := flag.String("accesskey", "", "The access-key associated to use")

	flag.Parse()

	apiconfig  := util.NewApiConfiguration(*apiDomain, *apiBackend, *secret, *access)

	// Output some sensible information about operation.
	fmt.Printf("Listen :%s, apiConfiguration: %+v \n", *listen, apiconfig)

	// Create root-node in graph, and monkey-patch our configuration onto it.
	routeexpressions := new(util.List)

	// Start api-part. We have hard-boiled api-hostnames in here, to
	// match our own infrastructure. That is, requests going to apiDomain
	// are sent to those systems. We extract the return-code, to know
	// if we're supposed to check something ourselves.
	apiRoute := NewRouteExpression(fmt.Sprintf("https://api.%s", *apiDomain))
	apiProxyIntercept := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			interceptWriter := bufferedResponseWriter{w, 0, 0}
			defer interceptWriter.Flush()

			h.ServeHTTP(&interceptWriter, r)

			if r.Method != http.MethodGet && interceptWriter.HTTPStatus == http.StatusOK {
				log.Printf("Scheduling refresh, because of api-change. (%d)\n",
					interceptWriter.HTTPStatus)

				routeexpressions = new(util.List) // override
				routeexpressions.Insert(*apiRoute) // Copy the api-node.

				// Create root-node in graph, and monkey-patch our configuration onto it.
				if err := LoadConfiguration(apiconfig, routeexpressions); err != nil {
					fmt.Printf("Could not load configuration. Aborting.")
				}

			}
		})
	}
	apiProxyRoute := NewProxyTargetRule(util.Backend{Backend :*apiBackend}, 10)
	apiRoute.AddTargetRule(apiProxyIntercept(apiProxyRoute))
	routeexpressions.Insert(*apiRoute)

	// Create root-node in graph, and monkey-patch our configuration onto it.
	if err := LoadConfiguration(apiconfig, routeexpressions); err != nil {
		fmt.Printf("Could not load configuration. Aborting.")
	}

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

				}, []string{"X-Loadbalancer: Golang-Accelerator"}, "https"), *logToStdout))
	err = http.ListenAndServeTLS(*listen, "/etc/letsencrypt/live/clouddom.eu/fullchain.pem","/etc/letsencrypt/live/clouddom.eu/privkey.pem", nil)
	log.Fatal(err)
}
