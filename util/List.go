package util

import (
	"fmt"
	"errors"
)

type Predicate func(interface{}) bool

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
}

func (l *List) Find(pred func(interface{}) bool) (interface{}, error) {
	list := l.head
	for list != nil {
		if pred(list.key) {
			return list.key, nil
		}
		list = list.next
	}
	return nil, errors.New("FindTargetGRoupByRouteExpression: No routes found")
}


	//	routeExpression := list.key.(RouteExpression)
		// http(s)://somedomain.com:$PORT/Path == http(s)://somedomain.com:$PORT/Path

		//if strings.HasPrefix(fmt.Sprintf("%s://%s%s", req.URL.Scheme, req.Host, req.URL.Path),
		//	routeExpression.Path) {
		//	return routeExpression, nil
		//}

