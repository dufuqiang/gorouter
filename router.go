package gorouter

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var (
	defaultPattern = `[\w]+`
	idPattern      = `[\d]+`
	idKey          = `id`
	methods        = map[string]bool{
		http.MethodGet:    true,
		http.MethodPost:   true,
		http.MethodPut:    true,
		http.MethodDelete: true,
		http.MethodPatch:  true,
	}
)

type (
	// MiddlewareType is a public type that is used for middleware
	MiddlewareType func(next http.HandlerFunc) http.HandlerFunc
	// Router is a simple HTTP route multiplexer that parses a request path,
	// records any URL params, and executes an end handler.
	Router struct {
		prefix string
		// The middleware stack
		middleware []MiddlewareType
		// the tree routers
		trees map[string]*Tree
		// Custom route not found handler
		notFound http.HandlerFunc
		// PanicHandler for handling panic.
		PanicHandler func(w http.ResponseWriter, req *http.Request, err interface{})
	}
)

// New returns a newly initialized Router object that implements the Router
func New() *Router {
	return &Router{
		trees: make(map[string]*Tree),
	}
}

// GET adds the route `path` that matches a GET http method to
// execute the `handle` http.HandlerFunc.
func (router *Router) GET(path string, handle http.HandlerFunc) {
	router.Handle(http.MethodGet, path, handle)
}

// POST adds the route `path` that matches a POST http method to
// execute the `handle` http.HandlerFunc.
func (router *Router) POST(path string, handle http.HandlerFunc) {
	router.Handle(http.MethodPost, path, handle)
}

// DELETE adds the route `path` that matches a DELETE http method to
// execute the `handle` http.HandlerFunc.
func (router *Router) DELETE(path string, handle http.HandlerFunc) {
	router.Handle(http.MethodDelete, path, handle)
}

// PUT adds the route `path` that matches a PUT http method to
// execute the `handle` http.HandlerFunc.
func (router *Router) PUT(path string, handle http.HandlerFunc) {
	router.Handle(http.MethodPut, path, handle)
}

// PATCH adds the route `path` that matches a PATCH http method to
// execute the `handle` http.HandlerFunc.
func (router *Router) PATCH(path string, handle http.HandlerFunc) {
	router.Handle(http.MethodPatch, path, handle)
}

// Group define routes groups If there is a path prefix that use `prefix`
func (router *Router) Group(prefix string) *Router {
	return &Router{
		prefix:     prefix,
		trees:      router.trees,
		middleware: router.middleware,
	}
}

// NotFoundFunc registers a handler when the request route is not found
func (router *Router) NotFoundFunc(handler http.HandlerFunc) {
	router.notFound = handler
}

// Handle registers a new request handle with the given path and method.
func (router *Router) Handle(method string, path string, handle http.HandlerFunc) {
	if _, ok := methods[method]; !ok {
		panic(fmt.Errorf("invalid method"))
	}

	root := router.trees[method]
	if root == nil {
		root = NewTree()
		router.trees[method] = root
	}

	if router.prefix != "" {
		path = router.prefix + "/" + path
	}

	root.Add(path, handle, router.middleware...)
}

// GetParam return route param stored in r.
func GetParam(r *http.Request, key string) string {
	return GetAllParams(r)[key]
}

// contextKeyType is a private struct that is used for storing values in net.Context
type contextKeyType struct{}

// contextKey is the key that is used to store values in the net.Context for each request
var contextKey = contextKeyType{}

// paramsMapType is a private type that is used to store route params
type paramsMapType map[string]string

// GetAllParams return all route params stored in r.
func GetAllParams(r *http.Request) paramsMapType {
	values, ok := r.Context().Value(contextKey).(paramsMapType)
	if ok {
		return values
	}

	return nil
}

// ServeHTTP makes the router implement the http.Handler interface.
func (router *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestUrl := r.URL.Path

	if router.PanicHandler != nil {
		defer func() {
			if err := recover(); err != nil {
				router.PanicHandler(w, r, err)
			}
		}()
	}

	if _, ok := router.trees[r.Method]; !ok {
		panic(fmt.Errorf("Error method or method is not registered "))
	}

	nodes := router.trees[r.Method].Find(requestUrl, 0)

	if len(nodes) > 0 {
		node := nodes[0]

		if node.handle != nil {
			if node.path == requestUrl {
				handle(w, r, node.handle, node.middleware)
				return
			}

			if node.path == requestUrl[1:] {
				handle(w, r, node.handle, node.middleware)
				return
			}
		}
	}

	if len(nodes) == 0 {
		res := strings.Split(requestUrl, "/")
		prefix := res[1]

		nodes := router.trees[r.Method].Find(prefix, 1)

		for _, node := range nodes {
			handler := node.handle

			if handler != nil && node.path != requestUrl {

				if matchParamsMap, ok := router.matchAndParse(requestUrl, node.path); ok {
					ctx := context.WithValue(r.Context(), contextKey, matchParamsMap)
					r = r.WithContext(ctx)
					handle(w, r, handler, node.middleware)
					return
				}
			}
		}
	}

	router.HandleNotFound(w, r, router.middleware)
}

// Use appends a middleware handler to the middleware stack.
func (router *Router) Use(middleware ...MiddlewareType) {
	if len(middleware) > 0 {
		router.middleware = append(router.middleware, middleware...)
	}
}

// HandleNotFound registers a handler when the request route is not found
func (router *Router) HandleNotFound(w http.ResponseWriter, r *http.Request, middleware []MiddlewareType) {
	if router.notFound != nil {
		handle(w, r, router.notFound, middleware)
		return
	}
	http.NotFound(w, r)
}

// handle execute middleware chain
func handle(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc, middleware []MiddlewareType) {
	var baseHandler = handler
	for _, m := range middleware {
		baseHandler = m(baseHandler)
	}
	baseHandler(w, r)
}

// Match check if the request match the route Pattern
func (router *Router) Match(requestUrl string, path string) bool {
	_, ok := router.matchAndParse(requestUrl, path)
	return ok
}

// matchAndParse check if the request matches the route path and returns a map of the parsed
func (router *Router) matchAndParse(requestUrl string, path string) (paramsMapType, bool) {
	res := strings.Split(path, "/")

	var (
		matchName []string
		sTemp     string
	)

	matchParams := make(paramsMapType)

	for _, str := range res {

		if str == "" {
			continue
		}

		strLen := len(str)
		firstChar := str[0]
		lastChar := str[strLen-1]

		if string(firstChar) == "{" && string(lastChar) == "}" {
			matchStr := string(str[1 : strLen-1])
			res := strings.Split(matchStr, ":")

			matchName = append(matchName, res[0])

			sTemp = sTemp + "/" + "(" + res[1] + ")"
		} else if string(firstChar) == ":" {
			matchStr := str
			res := strings.Split(matchStr, ":")
			matchName = append(matchName, res[1])

			if res[1] == idKey {
				sTemp = sTemp + "/" + "(" + idPattern + ")"
			} else {
				sTemp = sTemp + "/" + "(" + defaultPattern + ")"
			}
		} else {
			sTemp = sTemp + "/" + str
		}
	}

	pattern := sTemp

	re := regexp.MustCompile(pattern)
	subMatch := re.FindSubmatch([]byte(requestUrl))

	if subMatch != nil {
		if string(subMatch[0]) == requestUrl {
			subMatch = subMatch[1:]
			for k, v := range subMatch {
				matchParams[matchName[k]] = string(v)
			}
			return matchParams, true
		}
	}

	return nil, false
}
