package main

import (
	"net/http/httptest"
	"testing"
	"fmt"
	"io/ioutil"
)

func TestFindTargetGroupByRouteExpression(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/test", nil)

	routeexpressions := new(List)
	routeexpressions.Insert (*NewRouteExpression("/", "http://localhost"))
	routeexpressions.Insert (*NewRouteExpression("/test", "http://localhost"))

	t.Logf("* Testing found functionality, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := routeexpressions.FindTargetGroupByRouteExpression(req)
	if err != nil {
		t.Errorf("Did not find proper %s" ,err)
	}
	t.Logf("Found %+v", rs)


}

func TestNotFindTargetGroupByRouteExpression(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/", nil)

	routeexpressions := new(List)
	routeexpressions.Insert (*NewRouteExpression("/", "https://localhost"))

	t.Logf("* Testing not-found functionality, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := routeexpressions.FindTargetGroupByRouteExpression(req)
	if err != nil {
		t.Logf("Did not find proper %s" ,err)
	}
	t.Logf("Found %+v", rs)


}

func TestCacheRulesFindTargetGroupByRouteExpression(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/", nil)
	res := httptest.NewRecorder()

	routeexpressions := new(List)
	route := NewRouteExpression("/", "http://localhost")
	route.AddTargetRule(&ContentTargetRule{ Content: "This is the end"})
	routeexpressions.Insert (*route)

	t.Logf("* Testing Rule chain, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := routeexpressions.FindTargetGroupByRouteExpression(req)
	if err != nil {
		t.Logf("Did not find proper %s" ,err)
	}
	t.Logf("Found %+v", rs)

	// Serve an example
	for _, element := range rs.Target {
		if (element.ServeHTTP(res, req)) {
			break;
		}
	}


	resp := res.Result()
	body, _ := ioutil.ReadAll(resp.Body)
	t.Logf("Status: %d\n", resp.StatusCode)
	t.Logf("Content-Type: %s\n", resp.Header.Get("Content-Type"))
	t.Logf("Body: %s\n", string(body))
}

func TestProxyRulesFindTargetGroupByRouteExpression(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost/", nil)
	res := httptest.NewRecorder()

	routeexpressions := new(List)
	route := NewRouteExpression("/", "http://localhost")
	route.AddTargetRule(&ProxyTargetRule{ Target: "https://www.tuxand.me"})
	routeexpressions.Insert (*route)

	t.Logf("* Testing Rule chain, Path: %s, Host: %s\n", req.URL.Path, fmt.Sprintf("%s://%s", req.URL.Scheme, req.URL.Host))
	rs, err := routeexpressions.FindTargetGroupByRouteExpression(req)
	if err != nil {
		t.Logf("Did not find proper %s" ,err)
	}

	// Serve an example
	for _, element := range rs.Target {
		if (element.ServeHTTP(res, req)) {
			break;
		}
	}

	resp := res.Result()
	body, _ := ioutil.ReadAll(resp.Body)

	t.Logf("Status: %d\n", resp.StatusCode)
	t.Logf("Content-Type: %s\n", resp.Header.Get("Content-Type"))
	t.Logf("Body: %s\n", string(body))
}
