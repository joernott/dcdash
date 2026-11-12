// This nested module excludes the frontend tree (including node_modules, where some
// npm packages ship .go files) from the dcdash Go module and from `go ... ./...`.
module github.com/joernott/dcdash/web

go 1.27
