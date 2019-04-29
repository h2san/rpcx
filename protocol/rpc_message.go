package protocol

type RPCMessage struct {
	*Header
	ServicePath   string
	ServiceMethod string
	Metadata      map[string]string
	Payload       []byte
	data          []byte
}

func (message RPCMessage)
