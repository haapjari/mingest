package server

import "net"

// Listeners returns the listeners Listen bound, so a test can break them.
func (s *Server) Listeners() (public, admin net.Listener) {
	return s.publicL, s.adminL
}
