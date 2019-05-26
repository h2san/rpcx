package httpx

import (
	"context"
	"encoding/json"
	"github.com/h2san/sanrpc/log"
	"github.com/h2san/sanrpc/protocol"
	"io/ioutil"
	"net/http"
	"reflect"

	"github.com/julienschmidt/httprouter"
)

//HTTProtocol 路由实现
type HTTProtocol struct {
	protocol.BaseService
	plugins httpxPlugin
	router httprouter.Router
}

func (p *HTTProtocol) AddPlugin(plugin interface{}){
	p.plugins.Add(plugin)
}

func (p *HTTProtocol) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.router.ServeHTTP(w,r)
}

func (p *HTTProtocol) routeHander(w http.ResponseWriter, r *http.Request,param httprouter.Params) {
	err := p.plugins.DoPreHandleHTTPRequest(w,r,param)
	if err!=nil{
		writeErrResponse(w,HTTP_PRE_HANDLER_ERR,err.Error())
		return
	}

	p.handle(w, r,param)

	err = p.plugins.DoPreHandleHTTPRequest(w,r,param)
	if err!=nil{
		writeErrResponse(w,HTTP_POST_HANDLER_ERR,err.Error())
		return
	}
}

func (p *HTTProtocol) RegisterService(service interface{}) error{
	p.router.GET("/rpc/:service/:method", p.routeHander)
	p.router.POST("/rpc/:service/:method/",p.routeHander)
	return p.BaseService.RegisterService(service)
}

func (p *HTTProtocol) handle(w http.ResponseWriter,req *http.Request, param httprouter.Params)  {
	serviceName := param.ByName("service")
	methodName := param.ByName("method")
	log.Debug(serviceName,methodName)
	if serviceName=="" || methodName ==""{
		http.NotFound(w,req)
		return
	}

	service := p.ServiceMap[serviceName]

	if service == nil {
		http.NotFound(w,req)
		return
	}
	mtype := service.GetMethod(methodName)
	if mtype == nil {

		http.NotFound(w,req)
		return
	}
	data, err := ioutil.ReadAll(req.Body)
	if err != nil{
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	argv :=reflect.New(mtype.ArgType.Elem()).Interface()
	replyv :=reflect.New(mtype.ReplyType.Elem()).Interface()

	err = json.Unmarshal(data, argv)
	if err != nil{
		writeErrResponse(w,HTTPX_REQ_UNMARSHAL_ERR,err.Error())
		return
	}

	ctx := context.Background()

	if mtype.ArgType.Kind() != reflect.Ptr {
		err = service.Call(ctx, mtype, reflect.ValueOf(argv).Elem(), reflect.ValueOf(replyv))
	} else {
		err = service.Call(ctx, mtype, reflect.ValueOf(argv), reflect.ValueOf(replyv))
	}

	if err != nil {
		writeErrResponse(w,HTTP_REQ_HANDLE_ERR,err.Error())
		return
	}

	resData,err := json.Marshal(replyv)
	if err!= nil {
		writeErrResponse(w,HTTPX_RESP_MARSHAL_ERR,err.Error())
		return
	}

	w.Write(resData)
}