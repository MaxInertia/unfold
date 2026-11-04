package main

// The generated half of a gRPC server, in the shape protoc-gen-go-grpc emits
// it: a descriptor naming the service and its RPCs, and a registration helper
// that hands an implementation to the server. Written out here rather than
// imported, because a fixture that depended on google.golang.org/grpc would
// make these tests need the module cache.

// ServiceRegistrar is grpc.ServiceRegistrar: what a *grpc.Server is, seen from
// generated code.
type ServiceRegistrar interface {
	RegisterService(desc *ServiceDesc, impl any)
}

type MethodDesc struct{ MethodName string }

type StreamDesc struct{ StreamName string }

type ServiceDesc struct {
	ServiceName string
	Methods     []MethodDesc
	Streams     []StreamDesc
}

// ConversationService_ServiceDesc carries the join key: the same fully
// qualified names the generated client passes to Invoke on the other side.
var ConversationService_ServiceDesc = ServiceDesc{
	ServiceName: "conversation.v1.ConversationService",
	Methods:     []MethodDesc{{MethodName: "GetConversation"}},
	Streams:     []StreamDesc{{StreamName: "StreamConversation"}},
}

// RegisterConversationServiceServer is the generated helper. Its own body
// looks exactly like a registration, and isn't one: what it registers is its
// own parameter.
func RegisterConversationServiceServer(s ServiceRegistrar, srv ConversationServiceServer) {
	s.RegisterService(&ConversationService_ServiceDesc, srv)
}

// server stands in for *grpc.Server.
type server struct{}

func (s *server) RegisterService(desc *ServiceDesc, impl any) {}
