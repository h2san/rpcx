package httpx

import "net/http"
import "github.com/julienschmidt/httprouter"

type Plugin interface {

}

type (
	PreHandleHTTPRequestPlugin interface {
		PreHandleHTTPRequest(w http.ResponseWriter,req *http.Request, param httprouter.Params) error
	}

	PostHandleHTTPRequestPlugin interface {
		PostHandleHTTPRequest(w http.ResponseWriter,req *http.Request, param httprouter.Params)error
	}
)

type httpxPlugin struct {
	plugins []Plugin
}

func (p *httpxPlugin) DoPreHandleHTTPRequest(w http.ResponseWriter,req *http.Request, param httprouter.Params)error{
	for i := range p.plugins {
		if plugin, ok := p.plugins[i].(PreHandleHTTPRequestPlugin); ok {
			err := plugin.PreHandleHTTPRequest(w,req,param)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *httpxPlugin) DoPostHandleHTTPRequest(w http.ResponseWriter,req *http.Request, param httprouter.Params)error{
	for i := range p.plugins {
		if plugin, ok := p.plugins[i].(PostHandleHTTPRequestPlugin); ok {
			err := plugin.PostHandleHTTPRequest(w,req,param)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Add adds a plugin.
func (p *httpxPlugin) Add(plugin Plugin) {
	p.plugins = append(p.plugins, plugin)
}

// Remove removes a plugin by it's name.
func (p *httpxPlugin) Remove(plugin Plugin) {
	if p.plugins == nil {
		return
	}

	var plugins []Plugin
	for _, p := range p.plugins {
		if p != plugin {
			plugins = append(plugins, p)
		}
	}

	p.plugins = plugins
}
