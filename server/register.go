package server

func (s *Server) RegisterService(service interface{}){
	if s.protocol != nil{
		err := s.protocol.RegisterService(service)
		if err!= nil{
			panic(err)
		}
	}
	if s.httpHandler != nil{
		err := s.httpHandler.RegisterService(service)
		if err!= nil{
			panic(err)
		}
	}

}
