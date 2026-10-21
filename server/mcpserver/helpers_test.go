package mcpserver

import "github.com/tkodcumpeg4/zorven/shared/mcptools"

func reqOf(method, path, project string) mcptools.Request {
	return mcptools.Request{Method: method, Path: path, Project: project}
}
