package share

const (
	// DefaultRPCPath is used by ServeHTTP.
	DefaultRPCPath = "/_sanrpc_"
	// AuthKey is used in metadata.
	AuthKey = "__AUTH"
)



// ContextKey defines key type in context.
type ContextKey string

// ReqMetaDataKey is used to set metatdata in context of requests.
var ReqMetaDataKey = ContextKey("__req_metadata")

// ResMetaDataKey is used to set metatdata in context of responses.
var ResMetaDataKey = ContextKey("__res_metadata")

var CallSerializeType = ContextKey("__call_serialize_type")
var CallCompressType = ContextKey("__call_compress_type")