package main

// The generated half of a gRPC server, in the shape protoc-gen-go-grpc emits
// it. Written out rather than imported: a fixture that depended on
// google.golang.org/grpc would make these tests need the module cache, and
// nothing here is about the real package — the descriptor's *shape* is what
// carries the join key.

// ServiceRegistrar is grpc.ServiceRegistrar: what a *grpc.Server is, seen
// from generated code.
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

// NotesService_ServiceDesc names a service no proto in this repo declares.
// The registration is the only statement that this repo serves it.
var NotesService_ServiceDesc = ServiceDesc{
	ServiceName: "notes.v1.NotesService",
	Methods:     []MethodDesc{{MethodName: "AddNote"}},
	Streams:     []StreamDesc{{StreamName: "WatchNotes"}},
}

// RegisterNotesServiceServer is the generated helper. Its body looks exactly
// like a registration and is not one: what it registers is its own parameter.
func RegisterNotesServiceServer(s ServiceRegistrar, srv NotesServiceServer) {
	s.RegisterService(&NotesService_ServiceDesc, srv)
}

// ConversationService_ServiceDesc names a service the manifest declares as
// well, so the two tiers meet on the same key.
var ConversationService_ServiceDesc = ServiceDesc{
	ServiceName: "conversation.v1.ConversationService",
	Methods:     []MethodDesc{{MethodName: "GetConversation"}},
}

// MaintenanceService_ServiceDesc is declared in a proto excluded from SDK
// generation: implemented here, callable by nobody else.
var MaintenanceService_ServiceDesc = ServiceDesc{
	ServiceName: "conversation.v1.MaintenanceService",
	Methods:     []MethodDesc{{MethodName: "Purge"}},
}

// server stands in for *grpc.Server.
type server struct{}

func (s *server) RegisterService(desc *ServiceDesc, impl any) {}
