package codec

// SerializeType defines serialization type of payload.
type SerializeType byte

const (
	// SerializeNone uses raw []byte and don't serialize/deserialize
	SerializeNone SerializeType = iota
	// JSON for payload.
	JSON
	// ProtoBuffer for payload.
	ProtoBuffer
)

var (
	// Codecs are codecs supported by rpcx. You can add customized codecs in Codecs.
	Codecs = map[SerializeType]Codec{
		SerializeNone: &ByteCodec{},
		JSON:          &JSONCodec{},
		ProtoBuffer:   &PBCodec{},
	}
)

// RegisterCodec register customized codec.
func RegisterCodec(t SerializeType, c Codec) {
	Codecs[t] = c
}


// CompressType defines decompression type.
type CompressType byte

const (
	// None does not compress.
	CompressNone CompressType = iota
	// Gzip uses gzip compression.
	Gzip
)
