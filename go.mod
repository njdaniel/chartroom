module github.com/njdaniel/chartroom

go 1.25.0

// web/ is the browser client (Node). The go command must not treat Go files
// that npm packages ship in web/node_modules as packages of this module.
ignore ./web
