package main

import (
	"net/http/httptest"
	"testing"
)

func TestFindTargetGroupByRouteExpression(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)

	routeexpressions := new(List)
	routeexpressions.Insert (RouteExpression{ Path: "/", Host: "localhost" })

	rs, err := routeexpressions.FindTargetGroupByRouteExpression(req); 
	if err != nil {
		t.Errorf("Did not find proper %s" ,err)
	}

	t.Logf("Found %+v", rs)
}
