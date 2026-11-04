// Package main is a service that says what it serves by registering it, the
// way every Go gRPC server does, rather than by pointing at a proto.
package main

import "context"

// NotesServiceServer is the generated server interface.
type NotesServiceServer interface {
	AddNote(ctx context.Context) error
	WatchNotes(ctx context.Context) error
}

type NotesServer struct{}

func (n *NotesServer) AddNote(ctx context.Context) error    { n.store(); return nil }
func (n *NotesServer) WatchNotes(ctx context.Context) error { return nil }

// store is reached only by serving AddNote.
func (n *NotesServer) store() {}

// Conversation is the implementation of the service the manifest declares.
type Conversation struct{}

func (c *Conversation) GetConversation(ctx context.Context) error { return nil }

// Maintenance implements the service declared in the excluded proto.
type Maintenance struct{}

func (m *Maintenance) Purge(ctx context.Context) error { return nil }

func main() {
	s := &server{}
	RegisterNotesServiceServer(s, &NotesServer{})
	// Registered without a generated helper, which is the other shape a
	// registration takes.
	s.RegisterService(&ConversationService_ServiceDesc, &Conversation{})
	s.RegisterService(&MaintenanceService_ServiceDesc, &Maintenance{})
}
