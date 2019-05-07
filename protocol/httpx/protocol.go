package httpx

import (
	"github.com/julienschmidt/httprouter"
	"net/http"
)

type HTTProtocol struct {
	Route httprouter.Router
}

func (p *HTTProtocol) ServeHTTP(w http.ResponseWriter, r *http.Request){
	p.Route.ServeHTTP(w,r)
}



