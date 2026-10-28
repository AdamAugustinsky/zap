package composer

import "gonativeui/rexui"

// Editor is the composer's text: the scripted session of cmd/zapfast -bench
// types into it with Replace, as a person's keys would change it.
func (s *Screen) Editor() *rexui.ComposerState { return s.st }
