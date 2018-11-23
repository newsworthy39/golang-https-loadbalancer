package main

import (
	"net/http/httptest"
	"testing"
	"fmt"
	"io/ioutil"
	"github.com/newsworthy39/golang-https-loadbalancer/util"
	sdk "github.com/newsworthy39/golang-clouddom-sdk"
)

func TestFindTargetGroupByRouteExpression(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/test", nil)

	routeexpressions := new(util.List)
	routeexpressions.Insert (*NewRouteExpression("http://localhost/"))

	t.Logf("* Testing found functionality, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := FindTargetGroupByRouteExpression(routeexpressions, req)
	if err != nil {
		t.Errorf("Did not find proper %s" ,err)
	}
	t.Logf("Found %+v", rs)


}

func TestNotFindTargetGroupByRouteExpression(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/", nil)

	routeexpressions := new(util.List)
	routeexpressions.Insert (*NewRouteExpression("https://localhost/"))

	t.Logf("* Testing not-found functionality, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := FindTargetGroupByRouteExpression(routeexpressions, req)
	if err != nil {
		t.Logf("Did not find proper %s" ,err)
	}
	t.Logf("Found %+v", rs)


}

func TestCacheRulesFindTargetGroupByRouteExpression(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/", nil)
	res := httptest.NewRecorder()

	routeexpressions := new(util.List)
	route := NewRouteExpression("http://localhost/")
	route.AddTargetRule(NewContentTargetRule("This is the end"))
	routeexpressions.Insert (*route)

	t.Logf("* Testing Rule chain, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := FindTargetGroupByRouteExpression(routeexpressions, req)
	if err != nil {
		t.Logf("Did not find proper %s" ,err)
	}
	t.Logf("Found %+v", rs)

	rs.ServeHTTP(res,req)

	resp := res.Result()
	body, _ := ioutil.ReadAll(resp.Body)
	t.Logf("Status: %d\n", resp.StatusCode)
	t.Logf("Content-Type: %s\n", resp.Header.Get("Content-Type"))
	t.Logf("Body: %s\n", string(body))
}

func TestProxyRulesFindTargetGroupByRouteExpression(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/", nil)
	res := httptest.NewRecorder()

	routeexpressions := new(util.List)
	route := NewRouteExpression("http://localhost/")
	route.AddTargetRule(NewProxyTargetRule(sdk.Backend { Backend: "https://www.tuxand.me" }, 10))
	routeexpressions.Insert (*route)

	t.Logf("* Testing Rule chain, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := FindTargetGroupByRouteExpression(routeexpressions, req)
	if err != nil {
		t.Logf("Did not find proper %s" ,err)
	}

	rs.ServeHTTP(res,req)

	resp := res.Result()
	body, _ := ioutil.ReadAll(resp.Body)

	t.Logf("Status: %d\n", resp.StatusCode)
	t.Logf("Content-Type: %s\n", resp.Header.Get("Content-Type"))
	t.Logf("Body: %s\n", string(body))
}

func TestRedirectTargetRule(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/", nil)
	res := httptest.NewRecorder()

	routeexpressions := new(util.List)
	route := NewRouteExpression("http://localhost/")
	route.AddTargetRule(NewRedirectTargetRule("https://www.tuxand.me", 301))
	routeexpressions.Insert (*route)

	t.Logf("* Testing Rule chain, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := FindTargetGroupByRouteExpression(routeexpressions, req)
	if err != nil {
		t.Logf("Did not find proper %s" ,err)
	}

	rs.ServeHTTP(res,req)

	resp := res.Result()
	body, _ := ioutil.ReadAll(resp.Body)

	t.Logf("Status: %d\n", resp.StatusCode)
	t.Logf("Content-Type: %s\n", resp.Header.Get("Content-Type"))
	t.Logf("X-CacheRule: %s\n", resp.Header.Get("X-CacheRule"))
	t.Logf("Body: %s\n", string(body))
}

func TestCacheTargetRule(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/cache", nil)
	res := httptest.NewRecorder()

	routeexpressions := new(util.List)
	route := NewRouteExpression("http://localhost/cache")
	route.AddTargetRule(NewCacheTargetRule(sdk.Backend { Backend: "http://www.tuxand.me" }))
	routeexpressions.Insert (*route)

	t.Logf("* Testing Rule chain, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := FindTargetGroupByRouteExpression(routeexpressions, req)
	if err != nil {
		t.Logf("Did not find proper %s" ,err)
	}

	rs.ServeHTTP(res,req)

	resp := res.Result()
	body, _ := ioutil.ReadAll(resp.Body)

	t.Logf("Status: %d\n", resp.StatusCode)
	t.Logf("Content-Type: %s\n", resp.Header.Get("Content-Type"))
	t.Logf("Body: %s\n", string(body))
}
