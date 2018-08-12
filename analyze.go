package main

func analyze(p *parser) *analyzer {
	return &analyzer{p.name, p, make(chan node, 1)}
}

type analyzer struct {
	name   string
	parser *parser
	nodes  chan node
}

func (a *analyzer) Run() {
	a.emit(<-a.parser.nodes)
	close(a.nodes)
}

// emit passes an node back to the client.
func (a *analyzer) emit(node node) {
	a.nodes <- node
}
