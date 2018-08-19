package main

import (
	"errors"
	"fmt"

	"github.com/tyru/vain/node"
)

func parse(name string, inTokens <-chan token) *parser {
	return &parser{
		name:       name,
		inTokens:   inTokens,
		outNodes:   make(chan node.Node, 1),
		nextTokens: make([]token, 0, 16),
		saveEnvs:   make([]saveEnv, 0, 4),
	}
}

func (p *parser) Nodes() <-chan node.Node {
	return p.outNodes
}

type parser struct {
	name       string
	inTokens   <-chan token
	outNodes   chan node.Node
	token      *token  // next() sets read token to this.
	nextTokens []token // next() doesn't read from inTokens if len(nextTokens) > 0 .
	saveEnvs   []saveEnv
}

type saveEnv struct {
	unshifted  int
	prevTokens []token
}

type expr interface {
	node.Node
}

type statement interface {
	node.Node
}

func (p *parser) Run() {
	toplevel, err := p.acceptTopLevel()
	if err != nil {
		p.emit(err)
	} else {
		p.emit(toplevel)
	}
	close(p.outNodes) // No more nodes will be delivered.
}

// emit passes an node back to the client.
func (p *parser) emit(node node.Node) {
	p.outNodes <- node
}

// errorf returns an error token and terminates the scan
func (p *parser) errorf(format string, args ...interface{}) *node.ErrorNode {
	newargs := make([]interface{}, 0, len(args)+2)
	newargs = append(newargs, p.name, p.token.pos.Line(), p.token.pos.Col()+1)
	newargs = append(newargs, args...)
	err := fmt.Errorf("[parse] %s:%d:%d: "+format, newargs...)
	return node.NewErrorNode(err, p.token.pos)
}

// lexError is called when tokenError was given.
func (p *parser) lexError() *node.ErrorNode {
	return node.NewErrorNode(errors.New(p.token.val), p.token.pos)
}

// next returns the next token in the input.
// If the next token is EOF, backup it for the next next().
func (p *parser) next() *token {
	var t token
	if len(p.nextTokens) > 0 {
		t = p.nextTokens[len(p.nextTokens)-1]
		p.nextTokens = p.nextTokens[:len(p.nextTokens)-1]
	} else {
		t = <-p.inTokens
	}
	p.token = &t
	if t.typ == tokenEOF {
		p.backup()
	} else if len(p.saveEnvs) > 0 {
		env := &p.saveEnvs[len(p.saveEnvs)-1]
		env.prevTokens = append(env.prevTokens, t)
		if env.unshifted > 0 {
			env.unshifted--
		}
	}
	return &t
}

func (p *parser) unshift(t *token) {
	if len(p.saveEnvs) > 0 {
		env := &p.saveEnvs[len(p.saveEnvs)-1]
		env.unshifted++
	}
	p.nextTokens = append(p.nextTokens, *t)
}

func (p *parser) backup() {
	if len(p.saveEnvs) > 0 {
		env := &p.saveEnvs[len(p.saveEnvs)-1]
		if len(env.prevTokens) > 0 {
			env.prevTokens = env.prevTokens[:len(env.prevTokens)-1]
		}
	}
	p.nextTokens = append(p.nextTokens, *p.token)
}

func (p *parser) save() {
	p.saveEnvs = append(p.saveEnvs, saveEnv{0, make([]token, 0, 8)})
}

func (p *parser) forget() {
	p.saveEnvs = p.saveEnvs[:len(p.saveEnvs)-1]
}

func (p *parser) restore() {
	if len(p.saveEnvs) == 0 {
		return
	}
	env := &p.saveEnvs[len(p.saveEnvs)-1]
	p.saveEnvs = p.saveEnvs[:len(p.saveEnvs)-1]
	p.nextTokens = p.nextTokens[:len(p.nextTokens)-env.unshifted]
	for i := len(env.prevTokens) - 1; i >= 0; i-- {
		p.nextTokens = append(p.nextTokens, env.prevTokens[i])
	}
}

// peek returns but does not consume
// the next token in the input.
func (p *parser) peek() *token {
	t := p.next()
	p.backup()
	return t
}

// accept consumes the next token if its type is typ.
func (p *parser) accept(typ tokenType) bool {
	t := p.next()
	if t.typ == typ {
		return true
	}
	p.backup()
	return false
}

// acceptSpaces accepts 1*LF .
func (p *parser) acceptSpaces() bool {
	if p.accept(tokenNewline) {
		for p.accept(tokenNewline) {
		}
		return true
	}
	return false
}

// acceptBlanks accepts 1*( LF | comment | EOF ) .
func (p *parser) acceptBlanks() bool {
	t := p.next()
	switch t.typ {
	case tokenNewline:
	case tokenComment:
	case tokenEOF:
		return true
	default:
		p.backup()
		return false
	}
	for {
		t = p.next()
		switch t.typ {
		case tokenNewline:
		case tokenComment:
		case tokenEOF:
			return true
		default:
			p.backup()
			return true
		}
	}
}

// acceptIdentifierLike accepts token where canBeIdentifier(token) == true
func (p *parser) acceptIdentifierLike() bool {
	if p.canBeIdentifier(p.peek()) {
		p.next()
		return true
	}
	return false
}

// identifierLike := identifier | <value matches with '^\h\w*$'>
func (p *parser) canBeIdentifier(t *token) bool {
	if t.typ == tokenIdentifier {
		return true
	}
	if len(t.val) == 0 {
		return false
	}
	val := []rune(t.val)
	if !isWordHead(val[0]) {
		return false
	}
	for _, r := range val[1:] {
		if !isAlphaNumeric(r) {
			return false
		}
	}
	return true
}

type topLevelNode struct {
	body []node.Node
}

// Clone clones itself.
func (n *topLevelNode) Clone() node.Node {
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	return &topLevelNode{body}
}

func (n *topLevelNode) TerminalNode() node.Node {
	return n
}

func (n *topLevelNode) Position() *node.Pos {
	return nil
}

func (n *topLevelNode) IsExpr() bool {
	return false
}

func (p *parser) acceptTopLevel() (*node.PosNode, *node.ErrorNode) {
	pos := node.NewPos(0, 1, 0)
	toplevel := &topLevelNode{make([]node.Node, 0, 32)}
	for {
		n, err := p.acceptStmtOrExpr()
		if err != nil {
			if err == errParseEOF {
				err = nil
			}
			return node.NewPosNode(pos, toplevel), err
		}
		toplevel.body = append(toplevel.body, n)
	}
}

type commentNode struct {
	value string
}

// Clone clones itself.
func (n *commentNode) Clone() node.Node {
	return &commentNode{n.value}
}

func (n *commentNode) TerminalNode() node.Node {
	return n
}

func (n *commentNode) Position() *node.Pos {
	return nil
}

func (n *commentNode) IsExpr() bool {
	return false
}

func (n *commentNode) Value() string {
	return n.value[1:]
}

var errParseEOF = node.NewErrorNode(errors.New("EOF"), nil) // successful EOF

// statementOrExpression := *LF ( comment | statement | expr )
func (p *parser) acceptStmtOrExpr() (node.Node, *node.ErrorNode) {
	p.acceptSpaces()
	if p.accept(tokenEOF) {
		return nil, errParseEOF
	}
	if p.accept(tokenError) {
		return nil, p.lexError()
	}

	// Comment
	if p.accept(tokenComment) {
		n := node.NewPosNode(p.token.pos, &commentNode{p.token.val})
		return n, nil
	}

	// Statement
	switch p.peek().typ {
	case tokenFunc:
		return p.acceptFunction(false)
	case tokenConst:
		return p.acceptConstStatement()
	case tokenLet:
		return p.acceptLetStatement()
	case tokenReturn:
		return p.acceptReturnStatement()
	case tokenIf:
		return p.acceptIfStatement()
	case tokenWhile:
		return p.acceptWhileStatement()
	case tokenFor:
		return p.acceptForStatement()
	case tokenImport:
		fallthrough
	case tokenFrom:
		return p.acceptImportStatement()
	}

	// Expression
	return p.acceptExpr()
}

type constStatement struct {
	left  node.Node
	right expr
}

// Clone clones itself.
func (n *constStatement) Clone() node.Node {
	return &constStatement{n.left.Clone(), n.right.Clone()}
}

func (n *constStatement) TerminalNode() node.Node {
	return n
}

func (n *constStatement) Position() *node.Pos {
	return nil
}

func (n *constStatement) IsExpr() bool {
	return false
}

func (n *constStatement) Left() node.Node {
	return n.left
}

func (n *constStatement) Right() expr {
	return n.right
}

func (n *constStatement) GetLeftIdentifiers() []*identifierNode {
	return getLeftIdentifiers(n)
}

// constStatement := "const" assignExpr
func (p *parser) acceptConstStatement() (node.Node, *node.ErrorNode) {
	if !p.accept(tokenConst) {
		return nil, p.errorf("expected %s but got %s", tokenName(tokenConst), tokenName(p.peek().typ))
	}
	pos := p.token.pos
	assignPos, err := p.acceptAssignExpr()
	if err != nil {
		return nil, err
	}
	assign := assignPos.TerminalNode().(*assignExpr)
	n := node.NewPosNode(pos, &constStatement{assign.left, assign.right})
	return n, nil
}

type assignExpr struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *assignExpr) Clone() node.Node {
	return &assignExpr{n.left.Clone(), n.right.Clone()}
}

func (n *assignExpr) TerminalNode() node.Node {
	return n
}

func (n *assignExpr) Position() *node.Pos {
	return nil
}

func (n *assignExpr) IsExpr() bool {
	return true
}

func (n *assignExpr) Left() node.Node {
	return n.left
}

func (n *assignExpr) Right() expr {
	return n.right
}

func (n *assignExpr) GetLeftIdentifiers() []*identifierNode {
	return getLeftIdentifiers(n)
}

// assignExpr := assignLhs "=" expr
func (p *parser) acceptAssignExpr() (node.Node, *node.ErrorNode) {
	left, err := p.acceptAssignLHS()
	if err != nil {
		return nil, err
	}
	if !p.accept(tokenEqual) {
		return nil, p.errorf("expected %s but got %s", tokenName(tokenEqual), tokenName(p.peek().typ))
	}
	right, err := p.acceptExpr()
	if err != nil {
		return nil, err
	}
	var n node.Node = &assignExpr{left, right}
	if pos := left.Position(); pos != nil {
		n = node.NewPosNode(pos, n)
	}
	return n, nil
}

// assignLhs := identifier | destructuringAssignment
func (p *parser) acceptAssignLHS() (node.Node, *node.ErrorNode) {
	var left node.Node
	if p.accept(tokenIdentifier) {
		left = node.NewPosNode(p.token.pos, &identifierNode{p.token.val, true})
	} else if ids, listpos, err := p.acceptDestructuringAssignment(); err == nil {
		left = node.NewPosNode(listpos, &listNode{ids})
	} else {
		return nil, p.errorf(
			"expected %s or destructuring assignment but got %s",
			tokenName(tokenIdentifier),
			tokenName(p.peek().typ),
		)
	}
	return left, nil
}

// destructuringAssignment := "[" *blank
//                            *( identifierOrUnderscore *blank "," )
//                            identifierOrUnderscore *blank [ "," ]
//                          *blank "]"
// identifierOrUnderscore := identifier | "_"
func (p *parser) acceptDestructuringAssignment() ([]expr, *node.Pos, *node.ErrorNode) {
	if !p.accept(tokenSqOpen) {
		return nil, nil, p.errorf(
			"expected %s but got %s", tokenName(tokenLt), tokenName(p.peek().typ),
		)
	}
	pos := p.token.pos
	p.acceptBlanks()
	if p.accept(tokenSqClose) {
		return nil, nil, p.errorf("at least 1 identifier is needed")
	}

	ids := make([]expr, 0, 8)
	for {
		if !p.accept(tokenIdentifier) && !p.accept(tokenUnderscore) {
			return nil, nil, p.errorf(
				"expected %s or %s but got %s",
				tokenName(tokenIdentifier),
				tokenName(tokenUnderscore),
				tokenName(p.peek().typ),
			)
		}
		ids = append(ids, node.NewPosNode(p.token.pos, &identifierNode{p.token.val, true}))
		p.acceptBlanks()
		p.accept(tokenComma)
		p.acceptBlanks()
		if p.accept(tokenSqClose) {
			break
		}
	}
	return ids, pos, nil
}

type assignNode interface {
	Left() node.Node
	Right() expr
	GetLeftIdentifiers() []*identifierNode
}

func getLeftIdentifiers(n assignNode) []*identifierNode {
	switch left := n.Left().TerminalNode().(type) {
	case *listNode: // Destructuring
		ids := make([]*identifierNode, 0, len(left.value))
		for i := range left.value {
			if id, ok := left.value[i].TerminalNode().(*identifierNode); ok {
				ids = append(ids, id)
			}
		}
		return ids
	case *identifierNode:
		return []*identifierNode{left}
	default:
		return nil
	}
}

type letAssignStatement struct {
	left  node.Node
	right expr
}

// Clone clones itself.
func (n *letAssignStatement) Clone() node.Node {
	return &letAssignStatement{n.left.Clone(), n.right.Clone()}
}

func (n *letAssignStatement) TerminalNode() node.Node {
	return n
}

func (n *letAssignStatement) Position() *node.Pos {
	return nil
}

func (n *letAssignStatement) IsExpr() bool {
	return false
}

func (n *letAssignStatement) Left() node.Node {
	return n.left
}

func (n *letAssignStatement) Right() expr {
	return n.right
}

func (n *letAssignStatement) GetLeftIdentifiers() []*identifierNode {
	return getLeftIdentifiers(n)
}

type letDeclareStatement struct {
	left []argument
}

// Clone clones itself.
func (n *letDeclareStatement) Clone() node.Node {
	left := make([]argument, len(n.left))
	for i := range n.left {
		left[i] = *n.left[i].Clone()
	}
	return &letDeclareStatement{left}
}

func (n *letDeclareStatement) TerminalNode() node.Node {
	return n
}

func (n *letDeclareStatement) Position() *node.Pos {
	return nil
}

func (n *letDeclareStatement) IsExpr() bool {
	return false
}

// letStatement := letDeclareStatement / letAssignStatement
// letDeclareStatement := "let" variableAndType *( "," *blank variableAndType ) /
// letAssignStatement := "let" assignLhs "=" expr
func (p *parser) acceptLetStatement() (*node.PosNode, *node.ErrorNode) {
	if !p.accept(tokenLet) {
		return nil, p.errorf("expected %s but got %s", tokenName(tokenLet), tokenName(p.peek().typ))
	}
	pos := p.token.pos
	var left node.Node
	var right node.Node

	if p.accept(tokenIdentifier) { // for human
		id := p.token
		if p.acceptBlanks() {
			return nil, p.errorf(
				"expected type specifier but got %s", tokenName(p.peek().typ),
			)
		}
		p.unshift(id)
	}

	if arg, err := p.acceptVariableAndType(); err == nil {
		if id, ok := arg.left.TerminalNode().(*identifierNode); ok {
			if id.value == "_" {
				return nil, p.errorf("underscore variable can only be used in declaration")
			}
		} else {
			return nil, p.errorf("fatal: argument.left must contain *identifierNode")
		}
		left := []argument{*arg}
		for {
			if !p.accept(tokenComma) {
				break
			}
			arg, err := p.acceptVariableAndType()
			if err != nil {
				return nil, err
			}
			if id, ok := arg.left.TerminalNode().(*identifierNode); ok {
				if id.value == "_" {
					return nil, p.errorf("underscore variable can only be used in declaration")
				}
			} else {
				return nil, p.errorf("fatal: argument.left must contain *identifierNode")
			}
			left = append(left, *arg)
		}
		n := node.NewPosNode(pos, &letDeclareStatement{left})
		return n, nil
	} else if l, err := p.acceptAssignLHS(); err == nil {
		left = l
		if !p.accept(tokenEqual) {
			return nil, p.errorf(
				"expected %s but got %s",
				tokenName(tokenEqual),
				tokenName(p.peek().typ),
			)
		}
		var err *node.ErrorNode
		right, err = p.acceptExpr()
		if err != nil {
			return nil, err
		}
		n := node.NewPosNode(pos, &letAssignStatement{left, right})
		return n, nil
	} else {
		return nil, p.errorf(
			"expected variable(s) declaration or assignment but got %s",
			tokenName(p.peek().typ),
		)
	}
}

type returnStatement struct {
	left expr
}

// Clone clones itself.
func (n *returnStatement) Clone() node.Node {
	if n.left == nil {
		return &returnStatement{nil}
	}
	return &returnStatement{n.left.Clone()}
}

func (n *returnStatement) TerminalNode() node.Node {
	return n
}

func (n *returnStatement) Position() *node.Pos {
	return nil
}

func (n *returnStatement) IsExpr() bool {
	return false
}

// returnStatement := "return" ( expr | LF | EOF )
// And if tokenCClose is detected instead of expr,
// it must be empty return statement inside block.
func (p *parser) acceptReturnStatement() (node.Node, *node.ErrorNode) {
	if !p.accept(tokenReturn) {
		return nil, p.errorf("expected %s but got %s", tokenName(tokenReturn), tokenName(p.peek().typ))
	}
	ret := p.token
	if p.accept(tokenNewline) {
		return node.NewPosNode(ret.pos, &returnStatement{nil}), nil
	}
	t := p.peek()
	if t.typ == tokenEOF || t.typ == tokenCClose { // EOF or end of block
		return node.NewPosNode(ret.pos, &returnStatement{nil}), nil
	}
	expr, err := p.acceptExpr()
	if err != nil {
		return nil, err
	}
	return node.NewPosNode(ret.pos, &returnStatement{expr}), nil
}

type ifStatement struct {
	cond expr
	body []node.Node
	els  []node.Node
}

// Clone clones itself.
func (n *ifStatement) Clone() node.Node {
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	els := make([]node.Node, len(n.els))
	for i := range n.els {
		els[i] = n.els[i].Clone()
	}
	return &ifStatement{
		n.cond.Clone(), body, els,
	}
}

func (n *ifStatement) TerminalNode() node.Node {
	return n
}

func (n *ifStatement) Position() *node.Pos {
	return nil
}

func (n *ifStatement) IsExpr() bool {
	return false
}

// ifStatement := "if" *blank expr *blank block
//                [ *blank "else" *blank ( ifStatement | block ) ]
func (p *parser) acceptIfStatement() (node.Node, *node.ErrorNode) {
	if !p.accept(tokenIf) {
		return nil, p.errorf("expected if statement but got %s", tokenName(p.peek().typ))
	}
	p.acceptBlanks()
	pos := p.token.pos
	cond, err := p.acceptExpr()
	if err != nil {
		return nil, err
	}
	p.acceptBlanks()
	body, err := p.acceptBlock()
	if err != nil {
		return nil, err
	}
	var els []node.Node
	p.acceptBlanks()
	if p.accept(tokenElse) {
		p.acceptBlanks()
		if p.accept(tokenIf) {
			p.backup()
			ifstmt, err := p.acceptIfStatement()
			if err != nil {
				return nil, err
			}
			els = []node.Node{ifstmt}
		} else if p.accept(tokenCOpen) {
			p.backup()
			block, err := p.acceptBlock()
			if err != nil {
				return nil, err
			}
			els = block
		} else {
			return nil, p.errorf("expected if or block statement but got %s", tokenName(p.peek().typ))
		}
	}
	n := node.NewPosNode(pos, &ifStatement{cond, body, els})
	return n, nil
}

type whileStatement struct {
	cond expr
	body []node.Node
}

// Clone clones itself.
func (n *whileStatement) Clone() node.Node {
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	return &whileStatement{
		n.cond.Clone(), body,
	}
}

func (n *whileStatement) TerminalNode() node.Node {
	return n
}

func (n *whileStatement) Position() *node.Pos {
	return nil
}

func (n *whileStatement) IsExpr() bool {
	return false
}

// whileStatement := "while" *blank expr *blank block
func (p *parser) acceptWhileStatement() (node.Node, *node.ErrorNode) {
	if !p.accept(tokenWhile) {
		return nil, p.errorf("expected while statement but got %s", tokenName(p.peek().typ))
	}
	p.acceptBlanks()
	pos := p.token.pos
	cond, err := p.acceptExpr()
	if err != nil {
		return nil, err
	}
	p.acceptBlanks()
	body, err := p.acceptBlock()
	if err != nil {
		return nil, err
	}
	n := node.NewPosNode(pos, &whileStatement{cond, body})
	return n, nil
}

type forStatement struct {
	left  node.Node
	right expr
	body  []node.Node
}

// Clone clones itself.
func (n *forStatement) Clone() node.Node {
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	return &forStatement{
		n.left.Clone(), n.right.Clone(), body,
	}
}

func (n *forStatement) TerminalNode() node.Node {
	return n
}

func (n *forStatement) Position() *node.Pos {
	return nil
}

func (n *forStatement) IsExpr() bool {
	return false
}

func (n *forStatement) Left() node.Node {
	return n.left
}

func (n *forStatement) Right() expr {
	return n.right
}

func (n *forStatement) GetLeftIdentifiers() []*identifierNode {
	return getLeftIdentifiers(n)
}

// forStatement := "for" *blank assignLhs *blank "in" *blank expr *blank block
func (p *parser) acceptForStatement() (node.Node, *node.ErrorNode) {
	if !p.accept(tokenFor) {
		return nil, p.errorf("expected for statement but got %s", tokenName(p.peek().typ))
	}
	p.acceptBlanks()
	pos := p.token.pos
	left, err := p.acceptAssignLHS()
	if err != nil {
		return nil, err
	}
	p.acceptBlanks()
	if !p.accept(tokenIn) {
		return nil, p.errorf("expected %s but got %s", tokenName(tokenIn), tokenName(p.peek().typ))
	}
	p.acceptBlanks()
	right, err := p.acceptExpr()
	if err != nil {
		return nil, err
	}
	p.acceptBlanks()
	body, err := p.acceptBlock()
	if err != nil {
		return nil, err
	}
	n := node.NewPosNode(pos, &forStatement{left, right, body})
	return n, nil
}

// block := "{" *blank *( statementOrExpression *blank ) "}"
func (p *parser) acceptBlock() ([]node.Node, *node.ErrorNode) {
	if !p.accept(tokenCOpen) {
		return nil, p.errorf(
			"expected %s but got %s",
			tokenName(tokenCOpen),
			tokenName(p.peek().typ),
		)
	}
	var nodes []node.Node
	p.acceptBlanks()
	if !p.accept(tokenCClose) {
		nodes = make([]node.Node, 0, 16)
		for {
			stmt, err := p.acceptStmtOrExpr()
			if err != nil {
				return nil, err
			}
			p.acceptBlanks()
			nodes = append(nodes, stmt)
			if p.accept(tokenCClose) {
				break
			}
		}
	}
	return nodes, nil
}

type importStatement struct {
	pkg      vainString
	pkgAlias string
	fnlist   [][]string
}

// Clone clones itself.
func (n *importStatement) Clone() node.Node {
	fnlist := make([][]string, len(n.fnlist))
	for i := range n.fnlist {
		pair := make([]string, len(n.fnlist[i]))
		copy(pair, n.fnlist[i])
		fnlist[i] = pair
	}
	return &importStatement{
		n.pkg, n.pkgAlias, n.fnlist,
	}
}

func (n *importStatement) TerminalNode() node.Node {
	return n
}

func (n *importStatement) Position() *node.Pos {
	return nil
}

func (n *importStatement) IsExpr() bool {
	return false
}

// importStatement := "import" string [ "as" *blank identifier ] |
//                    "from" string "import" <importFunctionList>
func (p *parser) acceptImportStatement() (*node.PosNode, *node.ErrorNode) {
	if p.accept(tokenImport) {
		pos := p.token.pos
		if !p.accept(tokenString) {
			return nil, p.errorf("expected %s but got %s", tokenName(tokenString), tokenName(p.peek().typ))
		}
		pkg := vainString(p.token.val)
		var pkgAlias string
		if p.accept(tokenAs) {
			p.acceptBlanks()
			if !p.accept(tokenIdentifier) {
				return nil, p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
			}
			pkgAlias = p.token.val
		}
		stmt := node.NewPosNode(pos, &importStatement{pkg, pkgAlias, nil})
		return stmt, nil

	} else if p.accept(tokenFrom) {
		pos := p.token.pos
		if !p.accept(tokenString) {
			return nil, p.errorf("expected %s but got %s", tokenName(tokenString), tokenName(p.peek().typ))
		}
		pkg := vainString(p.token.val)
		if !p.accept(tokenImport) {
			return nil, p.errorf("expected %s but got %s", tokenName(tokenImport), tokenName(p.peek().typ))
		}
		fnlist, err := p.acceptImportFunctionList()
		if err != nil {
			return nil, err
		}
		stmt := node.NewPosNode(pos, &importStatement{pkg, "", fnlist})
		return stmt, nil
	}

	return nil, p.errorf(
		"expected %s or %s but got %s",
		tokenName(tokenImport), tokenName(tokenFrom), tokenName(p.peek().typ),
	)
}

// importFunctionList = importFunctionListItem *( *blank "," *blank importFunctionListItem )
// importFunctionListItem := identifier [ "as" *blank identifier ]
func (p *parser) acceptImportFunctionList() ([][]string, *node.ErrorNode) {
	fnlist := make([][]string, 0, 1)
	for {
		if !p.accept(tokenIdentifier) {
			return nil, p.errorf(
				"expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ),
			)
		}
		orig := p.token.val
		to := orig

		if p.accept(tokenAs) {
			p.acceptBlanks()
			if !p.accept(tokenIdentifier) {
				return nil, p.errorf(
					"expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ),
				)
			}
			to = p.token.val
			fnlist = append(fnlist, []string{orig, to})
		} else {
			fnlist = append(fnlist, []string{orig})
		}

		p.acceptBlanks()
		if !p.accept(tokenComma) {
			break
		}
		p.acceptBlanks()
	}

	return fnlist, nil
}

type funcStmtOrExpr struct {
	declare    *funcDeclareStatement
	bodyIsStmt bool
	body       []node.Node
	isExpr     bool
}

// Clone clones itself.
func (n *funcStmtOrExpr) Clone() node.Node {
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	return &funcStmtOrExpr{
		n.declare.Clone().(*funcDeclareStatement),
		n.bodyIsStmt,
		body,
		n.isExpr,
	}
}

func (n *funcStmtOrExpr) TerminalNode() node.Node {
	return n
}

func (n *funcStmtOrExpr) Position() *node.Pos {
	return nil
}

func (n *funcStmtOrExpr) IsExpr() bool {
	return n.isExpr
}

// function := funcStmtOrExpr | funcDeclareStatement
// funcStmtOrExpr := funcDeclare ( expr1 | block )
// funcDeclareStatement := funcDeclare ( LF | EOF )
func (p *parser) acceptFunction(isExpr bool) (*node.PosNode, *node.ErrorNode) {
	declare, pos, err := p.acceptFuncDeclare()
	if err != nil {
		return nil, err
	}

	// No body, declaration only.
	t := p.peek()
	if t.typ == tokenNewline || t.typ == tokenEOF {
		return node.NewPosNode(pos, declare), nil
	}

	var bodyIsStmt bool
	var body []node.Node

	// Body
	if p.accept(tokenCOpen) {
		p.backup()
		bodyIsStmt = true
		block, err := p.acceptBlock()
		if err != nil {
			return nil, err
		}
		body = block
	} else {
		expr, err := p.acceptExpr()
		if err != nil {
			return nil, err
		}
		body = []node.Node{expr}
	}

	funcNode := &funcStmtOrExpr{
		declare,
		bodyIsStmt,
		body,
		isExpr,
	}
	return node.NewPosNode(pos, funcNode), nil
}

type funcDeclareStatement struct {
	mods    []string
	name    string
	args    []argument
	retType string
}

// Clone clones itself.
func (n *funcDeclareStatement) Clone() node.Node {
	mods := make([]string, len(n.mods))
	copy(mods, n.mods)
	args := make([]argument, len(n.args))
	for i := range n.args {
		args[i] = *n.args[i].Clone()
	}
	return &funcDeclareStatement{
		mods, n.name, args, n.retType,
	}
}

func (n *funcDeclareStatement) TerminalNode() node.Node {
	return n
}

func (n *funcDeclareStatement) Position() *node.Pos {
	return nil
}

func (n *funcDeclareStatement) IsExpr() bool {
	return false
}

// funcDeclare := "func" [ funcModifierList ] [ identifier ] functionCallSignature
func (p *parser) acceptFuncDeclare() (*funcDeclareStatement, *node.Pos, *node.ErrorNode) {
	if !p.accept(tokenFunc) {
		return nil, nil, p.errorf(
			"expected %s but got %s",
			tokenName(tokenFunc),
			tokenName(p.peek().typ),
		)
	}
	pos := p.token.pos

	var mods []string
	var name string
	var args []argument
	var retType string
	var err *node.ErrorNode

	// Modifiers
	if p.accept(tokenLt) {
		p.backup()
		mods, err = p.acceptModifiers()
		if err != nil {
			return nil, nil, err
		}
	}

	// Function name (if empty, this is an expression not a statement)
	if p.accept(tokenIdentifier) {
		name = p.token.val
	}

	// functionCallSignature
	args, retType, err = p.acceptFunctionCallSignature()
	if err != nil {
		return nil, nil, err
	}

	f := &funcDeclareStatement{mods, name, args, retType}
	return f, pos, nil
}

// functionModifierList := "<" *blank
//                            *( functionModifier *blank "," )
//                            functionModifier *blank [ "," ]
//                          *blank ">"
func (p *parser) acceptModifiers() ([]string, *node.ErrorNode) {
	if !p.accept(tokenLt) {
		return nil, p.errorf(
			"expected %s but got %s", tokenName(tokenLt), tokenName(p.peek().typ),
		)
	}
	p.acceptBlanks()
	if p.accept(tokenGt) {
		return nil, p.errorf("at least 1 modifier is needed")
	}
	mods := make([]string, 0, 8)
	for {
		if !p.acceptFunctionModifier() {
			return nil, p.errorf(
				"expected function modifier but got %s", tokenName(p.peek().typ),
			)
		}
		mods = append(mods, p.token.val)
		p.acceptBlanks()
		p.accept(tokenComma)
		p.acceptBlanks()
		if p.accept(tokenGt) {
			break
		}
	}
	return mods, nil
}

// functionModifier := "noabort" | "autoload" | "global" | "range" | "dict" | "closure"
func (p *parser) acceptFunctionModifier() bool {
	if !p.accept(tokenIdentifier) {
		return false
	}
	switch p.token.val {
	case "noabort":
	case "autoload":
	case "global":
	case "range":
	case "dict":
	case "closure":
	default:
		return false
	}
	return true
}

// functionCallSignature := "(" *blank
//                            *( functionArgument *blank [ "," ] *blank )
//                          ")" [ ":" type ]
func (p *parser) acceptFunctionCallSignature() ([]argument, string, *node.ErrorNode) {
	if !p.accept(tokenPOpen) {
		return nil, "", p.errorf(
			"expected %s but got %s", tokenName(tokenPOpen), tokenName(p.peek().typ),
		)
	}
	p.acceptBlanks()

	var args []argument
	if !p.accept(tokenPClose) {
		args = make([]argument, 0, 8)
		for {
			arg, err := p.acceptFunctionArgument()
			if err != nil {
				return nil, "", err
			}
			args = append(args, *arg)
			p.acceptBlanks()
			p.accept(tokenComma)
			p.acceptBlanks()
			if p.accept(tokenPClose) {
				break
			}
		}
	}

	var retType string
	if p.accept(tokenColon) {
		var err *node.ErrorNode
		retType, err = p.acceptType()
		if err != nil {
			return nil, "", err
		}
	}
	return args, retType, nil
}

type argument struct {
	left       node.Node
	typ        string
	defaultVal expr
}

func (n *argument) Clone() *argument {
	var left node.Node
	if n.left != nil {
		left = n.left.Clone()
	}
	var defaultVal expr
	if n.defaultVal != nil {
		defaultVal = n.defaultVal.Clone()
	}
	return &argument{left, n.typ, defaultVal}
}

// variableAndType := identifier ":" *blanks type
func (p *parser) acceptVariableAndType() (*argument, *node.ErrorNode) {
	if !p.accept(tokenIdentifier) {
		return nil, p.errorf(
			"expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ),
		)
	}
	idToken := p.token
	left := node.NewPosNode(p.token.pos, &identifierNode{p.token.val, true})

	if !p.accept(tokenColon) {
		p.unshift(idToken)
		return nil, p.errorf(
			"expected %s but got %s", tokenName(tokenColon), tokenName(p.peek().typ),
		)
	}

	p.acceptBlanks()
	typ, err := p.acceptType()
	if err != nil {
		p.unshift(idToken)
		return nil, err
	}
	return &argument{left, typ, nil}, nil
}

// functionArgument := identifier ":" *blanks type /
//                     identifier "=" *blanks expr
func (p *parser) acceptFunctionArgument() (*argument, *node.ErrorNode) {
	var typ string

	if !p.accept(tokenIdentifier) {
		return nil, p.errorf(
			"expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ),
		)
	}
	left := node.NewPosNode(p.token.pos, &identifierNode{p.token.val, true})

	if p.accept(tokenColon) {
		p.acceptBlanks()
		var err *node.ErrorNode
		typ, err = p.acceptType()
		if err != nil {
			return nil, err
		}
		return &argument{left, typ, nil}, nil
	} else if p.accept(tokenEqual) {
		p.acceptBlanks()
		expr, err := p.acceptExpr()
		if err != nil {
			return nil, err
		}
		return &argument{left, "", expr}, nil
	}

	return nil, p.errorf(
		"expected %s or %s but got %s",
		tokenName(tokenColon),
		tokenName(tokenEqual),
		tokenName(p.peek().typ),
	)
}

// TODO: Complex type like array, dictionary, generics...
// type := identifier
func (p *parser) acceptType() (string, *node.ErrorNode) {
	if !p.accept(tokenIdentifier) {
		return "", p.errorf(
			"expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ),
		)
	}
	return p.token.val, nil
}

func (p *parser) acceptExpr() (expr, *node.ErrorNode) {
	return p.acceptExpr0()
}

// expr0 := assignExpr | expr1
func (p *parser) acceptExpr0() (expr, *node.ErrorNode) {
	p.save()
	if assign, err := p.acceptAssignExpr(); err == nil {
		p.forget()
		return assign, nil
	}
	p.restore()
	return p.acceptExpr1()
}

type ternaryNode struct {
	cond  expr
	left  expr
	right expr
}

// Clone clones itself.
func (n *ternaryNode) Clone() node.Node {
	return &ternaryNode{n.cond.Clone(), n.left.Clone(), n.right.Clone()}
}

func (n *ternaryNode) TerminalNode() node.Node {
	return n
}

func (n *ternaryNode) Position() *node.Pos {
	return nil
}

func (n *ternaryNode) IsExpr() bool {
	return true
}

// expr1 := expr2 [ "?" *blank expr1 *blank ":" *blank expr1 ]
func (p *parser) acceptExpr1() (expr, *node.ErrorNode) {
	left, err := p.acceptExpr2()
	if err != nil {
		return nil, err
	}
	if p.accept(tokenQuestion) {
		p.acceptBlanks()
		expr, err := p.acceptExpr1()
		if err != nil {
			return nil, err
		}
		p.acceptBlanks()
		if !p.accept(tokenColon) {
			return nil, p.errorf(
				"expected %s but got %s", tokenName(tokenColon), tokenName(p.peek().typ),
			)
		}
		p.acceptBlanks()
		right, err := p.acceptExpr1()
		if err != nil {
			return nil, err
		}
		left = node.NewPosNode(p.token.pos, &ternaryNode{left, expr, right})
	}
	return left, nil
}

type binaryOpNode interface {
	Left() node.Node
	Right() node.Node
}

type orNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *orNode) Clone() node.Node {
	return &orNode{n.left.Clone(), n.right.Clone()}
}

func (n *orNode) TerminalNode() node.Node {
	return n
}

func (n *orNode) Position() *node.Pos {
	return nil
}

func (n *orNode) IsExpr() bool {
	return true
}

func (n *orNode) Left() node.Node {
	return n.left
}

func (n *orNode) Right() node.Node {
	return n.right
}

// expr2 := expr3 *( "||" *blank expr3 )
func (p *parser) acceptExpr2() (expr, *node.ErrorNode) {
	left, err := p.acceptExpr3()
	if err != nil {
		return nil, err
	}
	for {
		if p.accept(tokenOrOr) {
			pos := p.token.pos
			n := &orNode{left, nil}
			p.acceptBlanks()
			right, err := p.acceptExpr3()
			if err != nil {
				return nil, err
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else {
			break
		}
	}
	return left, nil
}

type andNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *andNode) Clone() node.Node {
	return &andNode{n.left.Clone(), n.right.Clone()}
}

func (n *andNode) TerminalNode() node.Node {
	return n
}

func (n *andNode) Position() *node.Pos {
	return nil
}

func (n *andNode) IsExpr() bool {
	return true
}

func (n *andNode) Left() node.Node {
	return n.left
}

func (n *andNode) Right() node.Node {
	return n.right
}

// expr3 := expr4 *( "&&" *blank expr4 )
func (p *parser) acceptExpr3() (expr, *node.ErrorNode) {
	left, err := p.acceptExpr4()
	if err != nil {
		return nil, err
	}
	for {
		if p.accept(tokenAndAnd) {
			pos := p.token.pos
			n := &andNode{left, nil}
			p.acceptBlanks()
			right, err := p.acceptExpr4()
			if err != nil {
				return nil, err
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else {
			break
		}
	}
	return left, nil
}

type equalNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *equalNode) Clone() node.Node {
	return &equalNode{n.left.Clone(), n.right.Clone()}
}

func (n *equalNode) TerminalNode() node.Node {
	return n
}

func (n *equalNode) Position() *node.Pos {
	return nil
}

func (n *equalNode) IsExpr() bool {
	return true
}

func (n *equalNode) Left() node.Node {
	return n.left
}

func (n *equalNode) Right() node.Node {
	return n.right
}

type equalCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *equalCiNode) Clone() node.Node {
	return &equalCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *equalCiNode) TerminalNode() node.Node {
	return n
}

func (n *equalCiNode) Position() *node.Pos {
	return nil
}

func (n *equalCiNode) IsExpr() bool {
	return true
}

func (n *equalCiNode) Left() node.Node {
	return n.left
}

func (n *equalCiNode) Right() node.Node {
	return n.right
}

type nequalNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *nequalNode) Clone() node.Node {
	return &nequalNode{n.left.Clone(), n.right.Clone()}
}

func (n *nequalNode) TerminalNode() node.Node {
	return n
}

func (n *nequalNode) Position() *node.Pos {
	return nil
}

func (n *nequalNode) IsExpr() bool {
	return true
}

func (n *nequalNode) Left() node.Node {
	return n.left
}

func (n *nequalNode) Right() node.Node {
	return n.right
}

type nequalCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *nequalCiNode) Clone() node.Node {
	return &nequalCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *nequalCiNode) TerminalNode() node.Node {
	return n
}

func (n *nequalCiNode) Position() *node.Pos {
	return nil
}

func (n *nequalCiNode) IsExpr() bool {
	return true
}

func (n *nequalCiNode) Left() node.Node {
	return n.left
}

func (n *nequalCiNode) Right() node.Node {
	return n.right
}

type greaterNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *greaterNode) Clone() node.Node {
	return &greaterNode{n.left.Clone(), n.right.Clone()}
}

func (n *greaterNode) TerminalNode() node.Node {
	return n
}

func (n *greaterNode) Position() *node.Pos {
	return nil
}

func (n *greaterNode) IsExpr() bool {
	return true
}

func (n *greaterNode) Left() node.Node {
	return n.left
}

func (n *greaterNode) Right() node.Node {
	return n.right
}

type greaterCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *greaterCiNode) Clone() node.Node {
	return &greaterCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *greaterCiNode) TerminalNode() node.Node {
	return n
}

func (n *greaterCiNode) Position() *node.Pos {
	return nil
}

func (n *greaterCiNode) IsExpr() bool {
	return true
}

func (n *greaterCiNode) Left() node.Node {
	return n.left
}

func (n *greaterCiNode) Right() node.Node {
	return n.right
}

type gequalNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *gequalNode) Clone() node.Node {
	return &gequalNode{n.left.Clone(), n.right.Clone()}
}

func (n *gequalNode) TerminalNode() node.Node {
	return n
}

func (n *gequalNode) Position() *node.Pos {
	return nil
}

func (n *gequalNode) IsExpr() bool {
	return true
}

func (n *gequalNode) Left() node.Node {
	return n.left
}

func (n *gequalNode) Right() node.Node {
	return n.right
}

type gequalCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *gequalCiNode) Clone() node.Node {
	return &gequalCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *gequalCiNode) TerminalNode() node.Node {
	return n
}

func (n *gequalCiNode) Position() *node.Pos {
	return nil
}

func (n *gequalCiNode) IsExpr() bool {
	return true
}

func (n *gequalCiNode) Left() node.Node {
	return n.left
}

func (n *gequalCiNode) Right() node.Node {
	return n.right
}

type smallerNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *smallerNode) Clone() node.Node {
	return &smallerNode{n.left.Clone(), n.right.Clone()}
}

func (n *smallerNode) TerminalNode() node.Node {
	return n
}

func (n *smallerNode) Position() *node.Pos {
	return nil
}

func (n *smallerNode) IsExpr() bool {
	return true
}

func (n *smallerNode) Left() node.Node {
	return n.left
}

func (n *smallerNode) Right() node.Node {
	return n.right
}

type smallerCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *smallerCiNode) Clone() node.Node {
	return &smallerCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *smallerCiNode) TerminalNode() node.Node {
	return n
}

func (n *smallerCiNode) Position() *node.Pos {
	return nil
}

func (n *smallerCiNode) IsExpr() bool {
	return true
}

func (n *smallerCiNode) Left() node.Node {
	return n.left
}

func (n *smallerCiNode) Right() node.Node {
	return n.right
}

type sequalNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *sequalNode) Clone() node.Node {
	return &sequalNode{n.left.Clone(), n.right.Clone()}
}

func (n *sequalNode) TerminalNode() node.Node {
	return n
}

func (n *sequalNode) Position() *node.Pos {
	return nil
}

func (n *sequalNode) IsExpr() bool {
	return true
}

func (n *sequalNode) Left() node.Node {
	return n.left
}

func (n *sequalNode) Right() node.Node {
	return n.right
}

type sequalCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *sequalCiNode) Clone() node.Node {
	return &sequalCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *sequalCiNode) TerminalNode() node.Node {
	return n
}

func (n *sequalCiNode) Position() *node.Pos {
	return nil
}

func (n *sequalCiNode) IsExpr() bool {
	return true
}

func (n *sequalCiNode) Left() node.Node {
	return n.left
}

func (n *sequalCiNode) Right() node.Node {
	return n.right
}

type matchNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *matchNode) Clone() node.Node {
	return &matchNode{n.left.Clone(), n.right.Clone()}
}

func (n *matchNode) TerminalNode() node.Node {
	return n
}

func (n *matchNode) Position() *node.Pos {
	return nil
}

func (n *matchNode) IsExpr() bool {
	return true
}

func (n *matchNode) Left() node.Node {
	return n.left
}

func (n *matchNode) Right() node.Node {
	return n.right
}

type matchCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *matchCiNode) Clone() node.Node {
	return &matchCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *matchCiNode) TerminalNode() node.Node {
	return n
}

func (n *matchCiNode) Position() *node.Pos {
	return nil
}

func (n *matchCiNode) IsExpr() bool {
	return true
}

func (n *matchCiNode) Left() node.Node {
	return n.left
}

func (n *matchCiNode) Right() node.Node {
	return n.right
}

type noMatchNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *noMatchNode) Clone() node.Node {
	return &noMatchNode{n.left.Clone(), n.right.Clone()}
}

func (n *noMatchNode) TerminalNode() node.Node {
	return n
}

func (n *noMatchNode) Position() *node.Pos {
	return nil
}

func (n *noMatchNode) IsExpr() bool {
	return true
}

func (n *noMatchNode) Left() node.Node {
	return n.left
}

func (n *noMatchNode) Right() node.Node {
	return n.right
}

type noMatchCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *noMatchCiNode) Clone() node.Node {
	return &noMatchCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *noMatchCiNode) TerminalNode() node.Node {
	return n
}

func (n *noMatchCiNode) Position() *node.Pos {
	return nil
}

func (n *noMatchCiNode) IsExpr() bool {
	return true
}

func (n *noMatchCiNode) Left() node.Node {
	return n.left
}

func (n *noMatchCiNode) Right() node.Node {
	return n.right
}

type isNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *isNode) Clone() node.Node {
	return &isNode{n.left.Clone(), n.right.Clone()}
}

func (n *isNode) TerminalNode() node.Node {
	return n
}

func (n *isNode) Position() *node.Pos {
	return nil
}

func (n *isNode) IsExpr() bool {
	return true
}

func (n *isNode) Left() node.Node {
	return n.left
}

func (n *isNode) Right() node.Node {
	return n.right
}

type isCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *isCiNode) Clone() node.Node {
	return &isCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *isCiNode) TerminalNode() node.Node {
	return n
}

func (n *isCiNode) Position() *node.Pos {
	return nil
}

func (n *isCiNode) IsExpr() bool {
	return true
}

func (n *isCiNode) Left() node.Node {
	return n.left
}

func (n *isCiNode) Right() node.Node {
	return n.right
}

type isNotNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *isNotNode) Clone() node.Node {
	return &isNotNode{n.left.Clone(), n.right.Clone()}
}

func (n *isNotNode) TerminalNode() node.Node {
	return n
}

func (n *isNotNode) Position() *node.Pos {
	return nil
}

func (n *isNotNode) IsExpr() bool {
	return true
}

func (n *isNotNode) Left() node.Node {
	return n.left
}

func (n *isNotNode) Right() node.Node {
	return n.right
}

type isNotCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *isNotCiNode) Clone() node.Node {
	return &isNotCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *isNotCiNode) TerminalNode() node.Node {
	return n
}

func (n *isNotCiNode) Position() *node.Pos {
	return nil
}

func (n *isNotCiNode) IsExpr() bool {
	return true
}

func (n *isNotCiNode) Left() node.Node {
	return n.left
}

func (n *isNotCiNode) Right() node.Node {
	return n.right
}

// expr4 := expr5 "=="  *blank expr5 /
//          expr5 "==?" *blank expr5 /
//          expr5 "!="  *blank expr5 /
//          expr5 "!=?" *blank expr5 /
//          expr5 ">"   *blank expr5 /
//          expr5 ">?"  *blank expr5 /
//          expr5 ">="  *blank expr5 /
//          expr5 ">=?" *blank expr5 /
//          expr5 "<"   *blank expr5 /
//          expr5 "<?"  *blank expr5 /
//          expr5 "<="  *blank expr5 /
//          expr5 "<=?" *blank expr5 /
//          expr5 "=~"  *blank expr5 /
//          expr5 "=~?" *blank expr5 /
//          expr5 "!~"  *blank expr5 /
//          expr5 "!~?" *blank expr5 /
//          expr5 "is"  *blank expr5 /
//          expr5 "is?" *blank expr5 /
//          expr5 "isnot"  *blank expr5 /
//          expr5 "isnot?" *blank expr5 /
//          expr5
func (p *parser) acceptExpr4() (expr, *node.ErrorNode) {
	left, err := p.acceptExpr5()
	if err != nil {
		return nil, err
	}
	if p.accept(tokenEqEq) {
		pos := p.token.pos
		n := &equalNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenEqEqCi) {
		pos := p.token.pos
		n := &equalCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenNeq) {
		pos := p.token.pos
		n := &nequalNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenNeqCi) {
		pos := p.token.pos
		n := &nequalCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenGt) {
		pos := p.token.pos
		n := &greaterNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenGtCi) {
		pos := p.token.pos
		n := &greaterCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenGtEq) {
		pos := p.token.pos
		n := &gequalNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenGtEqCi) {
		pos := p.token.pos
		n := &gequalCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenLt) {
		pos := p.token.pos
		n := &smallerNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenLtCi) {
		pos := p.token.pos
		n := &smallerCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenLtEq) {
		pos := p.token.pos
		n := &sequalNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenLtEqCi) {
		pos := p.token.pos
		n := &sequalCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenMatch) {
		pos := p.token.pos
		n := &matchNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenMatchCi) {
		pos := p.token.pos
		n := &matchCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenNoMatch) {
		pos := p.token.pos
		n := &noMatchNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenNoMatchCi) {
		pos := p.token.pos
		n := &noMatchCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenIs) {
		pos := p.token.pos
		n := &isNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenIsCi) {
		pos := p.token.pos
		n := &isCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenIsNot) {
		pos := p.token.pos
		n := &isNotNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenIsNotCi) {
		pos := p.token.pos
		n := &isNotCiNode{left, nil}
		p.acceptBlanks()
		right, err := p.acceptExpr5()
		if err != nil {
			return nil, err
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	}
	return left, nil
}

type addNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *addNode) Clone() node.Node {
	return &addNode{n.left.Clone(), n.right.Clone()}
}

func (n *addNode) TerminalNode() node.Node {
	return n
}

func (n *addNode) Position() *node.Pos {
	return nil
}

func (n *addNode) IsExpr() bool {
	return true
}

func (n *addNode) Left() node.Node {
	return n.left
}

func (n *addNode) Right() node.Node {
	return n.right
}

type subtractNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *subtractNode) Clone() node.Node {
	return &subtractNode{n.left.Clone(), n.right.Clone()}
}

func (n *subtractNode) TerminalNode() node.Node {
	return n
}

func (n *subtractNode) Position() *node.Pos {
	return nil
}

func (n *subtractNode) IsExpr() bool {
	return true
}

func (n *subtractNode) Left() node.Node {
	return n.left
}

func (n *subtractNode) Right() node.Node {
	return n.right
}

// expr5 := expr6 1*( "+" *blank expr6 ) /
//          expr6 1*( "-" *blank expr6 ) /
//          expr6
func (p *parser) acceptExpr5() (expr, *node.ErrorNode) {
	left, err := p.acceptExpr6()
	if err != nil {
		return nil, err
	}
	for {
		if p.accept(tokenPlus) {
			pos := p.token.pos
			n := &addNode{left, nil}
			p.acceptBlanks()
			right, err := p.acceptExpr6()
			if err != nil {
				return nil, err
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else if p.accept(tokenMinus) {
			pos := p.token.pos
			n := &subtractNode{left, nil}
			p.acceptBlanks()
			right, err := p.acceptExpr6()
			if err != nil {
				return nil, err
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else {
			break
		}
	}
	return left, nil
}

type multiplyNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *multiplyNode) Clone() node.Node {
	return &multiplyNode{n.left.Clone(), n.right.Clone()}
}

func (n *multiplyNode) TerminalNode() node.Node {
	return n
}

func (n *multiplyNode) Position() *node.Pos {
	return nil
}

func (n *multiplyNode) IsExpr() bool {
	return true
}

func (n *multiplyNode) Left() node.Node {
	return n.left
}

func (n *multiplyNode) Right() node.Node {
	return n.right
}

type divideNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *divideNode) Clone() node.Node {
	return &divideNode{n.left.Clone(), n.right.Clone()}
}

func (n *divideNode) TerminalNode() node.Node {
	return n
}

func (n *divideNode) Position() *node.Pos {
	return nil
}

func (n *divideNode) IsExpr() bool {
	return true
}

func (n *divideNode) Left() node.Node {
	return n.left
}

func (n *divideNode) Right() node.Node {
	return n.right
}

type remainderNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *remainderNode) Clone() node.Node {
	return &remainderNode{n.left.Clone(), n.right.Clone()}
}

func (n *remainderNode) TerminalNode() node.Node {
	return n
}

func (n *remainderNode) Position() *node.Pos {
	return nil
}

func (n *remainderNode) IsExpr() bool {
	return true
}

func (n *remainderNode) Left() node.Node {
	return n.left
}

func (n *remainderNode) Right() node.Node {
	return n.right
}

// expr6 := expr7 1*( "*" *blank expr7 ) /
//          expr7 1*( "/" *blank expr7 ) /
//          expr7 1*( "%" *blank expr7 ) /
//          expr7
func (p *parser) acceptExpr6() (expr, *node.ErrorNode) {
	left, err := p.acceptExpr7()
	if err != nil {
		return nil, err
	}
	for {
		if p.accept(tokenStar) {
			pos := p.token.pos
			n := &multiplyNode{left, nil}
			p.acceptBlanks()
			right, err := p.acceptExpr7()
			if err != nil {
				return nil, err
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else if p.accept(tokenSlash) {
			pos := p.token.pos
			n := &divideNode{left, nil}
			p.acceptBlanks()
			right, err := p.acceptExpr7()
			if err != nil {
				return nil, err
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else if p.accept(tokenPercent) {
			pos := p.token.pos
			n := &remainderNode{left, nil}
			p.acceptBlanks()
			right, err := p.acceptExpr7()
			if err != nil {
				return nil, err
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else {
			break
		}
	}
	return left, nil
}

type unaryOpNode interface {
	Value() node.Node
}

type notNode struct {
	left expr
}

// Clone clones itself.
func (n *notNode) Clone() node.Node {
	return &notNode{n.left.Clone()}
}

func (n *notNode) TerminalNode() node.Node {
	return n
}

func (n *notNode) Position() *node.Pos {
	return nil
}

func (n *notNode) IsExpr() bool {
	return true
}

func (n *notNode) Value() node.Node {
	return n.left
}

type minusNode struct {
	left expr
}

// Clone clones itself.
func (n *minusNode) Clone() node.Node {
	return &minusNode{n.left.Clone()}
}

func (n *minusNode) TerminalNode() node.Node {
	return n
}

func (n *minusNode) Position() *node.Pos {
	return nil
}

func (n *minusNode) IsExpr() bool {
	return true
}

func (n *minusNode) Value() node.Node {
	return n.left
}

type plusNode struct {
	left expr
}

// Clone clones itself.
func (n *plusNode) Clone() node.Node {
	return &plusNode{n.left.Clone()}
}

func (n *plusNode) TerminalNode() node.Node {
	return n
}

func (n *plusNode) Position() *node.Pos {
	return nil
}

func (n *plusNode) IsExpr() bool {
	return true
}

func (n *plusNode) Value() node.Node {
	return n.left
}

// expr7 := "!" expr7 /
//          "-" expr7 /
//          "+" expr7 /
//          expr8
func (p *parser) acceptExpr7() (expr, *node.ErrorNode) {
	if p.accept(tokenNot) {
		left, err := p.acceptExpr7()
		if err != nil {
			return nil, err
		}
		n := node.NewPosNode(p.token.pos, &notNode{left})
		return n, nil
	} else if p.accept(tokenMinus) {
		left, err := p.acceptExpr7()
		if err != nil {
			return nil, err
		}
		n := node.NewPosNode(p.token.pos, &minusNode{left})
		return n, nil
	} else if p.accept(tokenPlus) {
		left, err := p.acceptExpr7()
		if err != nil {
			return nil, err
		}
		n := node.NewPosNode(p.token.pos, &plusNode{left})
		return n, nil
	} else {
		n, err := p.acceptExpr8()
		if err != nil {
			return nil, err
		}
		return n, nil
	}
}

type sliceNode struct {
	left  expr
	rlist []expr
}

// Clone clones itself.
func (n *sliceNode) Clone() node.Node {
	rlist := make([]expr, len(n.rlist))
	for i := range n.rlist {
		if n.rlist[i] != nil {
			rlist[i] = n.rlist[i].Clone()
		} else {
			rlist[i] = nil
		}
	}
	return &sliceNode{n.left.Clone(), rlist}
}

func (n *sliceNode) TerminalNode() node.Node {
	return n
}

func (n *sliceNode) Position() *node.Pos {
	return nil
}

func (n *sliceNode) IsExpr() bool {
	return true
}

type callNode struct {
	left  expr
	rlist []expr
}

// Clone clones itself.
func (n *callNode) Clone() node.Node {
	rlist := make([]expr, len(n.rlist))
	for i := range n.rlist {
		rlist[i] = n.rlist[i].Clone()
	}
	return &callNode{n.left.Clone(), rlist}
}

func (n *callNode) TerminalNode() node.Node {
	return n
}

func (n *callNode) Position() *node.Pos {
	return nil
}

func (n *callNode) IsExpr() bool {
	return true
}

type subscriptNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *subscriptNode) Clone() node.Node {
	return &subscriptNode{n.left.Clone(), n.right.Clone()}
}

func (n *subscriptNode) TerminalNode() node.Node {
	return n
}

func (n *subscriptNode) Position() *node.Pos {
	return nil
}

func (n *subscriptNode) IsExpr() bool {
	return true
}

func (n *subscriptNode) Left() node.Node {
	return n.left
}

func (n *subscriptNode) Right() node.Node {
	return n.right
}

type dotNode struct {
	left  expr
	right node.Node
}

// Clone clones itself.
func (n *dotNode) Clone() node.Node {
	return &dotNode{n.left.Clone(), n.right.Clone()}
}

func (n *dotNode) TerminalNode() node.Node {
	return n
}

func (n *dotNode) Position() *node.Pos {
	return nil
}

func (n *dotNode) IsExpr() bool {
	return true
}

func (n *dotNode) Left() node.Node {
	return n.left
}

func (n *dotNode) Right() node.Node {
	return n.right
}

type identifierNode struct {
	value     string
	isVarname bool
}

// Clone clones itself.
func (n *identifierNode) Clone() node.Node {
	return &identifierNode{n.value, n.isVarname}
}

func (n *identifierNode) TerminalNode() node.Node {
	return n
}

func (n *identifierNode) Position() *node.Pos {
	return nil
}

func (n *identifierNode) IsExpr() bool {
	return true
}

// expr8 := expr9 1*( "[" *blank expr1 *blank "]" ) /
//          expr9 1*( "[" *blank [ expr1 *blank ] ":" *blank [ expr1 *blank ] "]" ) /
//          expr9 1*( "." *blank identifierLike ) /
//          expr9 1*( "(" *blank [ expr1 *blank *( "," *blank expr1 *blank) [ "," ] ] *blank ")" ) /
//          expr9
func (p *parser) acceptExpr8() (expr, *node.ErrorNode) {
	left, err := p.acceptExpr9()
	if err != nil {
		return nil, err
	}
	for {
		if p.accept(tokenSqOpen) {
			npos := p.token.pos
			p.acceptBlanks()
			if p.accept(tokenColon) {
				n := &sliceNode{left, []expr{nil, nil}}
				p.acceptBlanks()
				if p.peek().typ != tokenSqClose {
					expr, err := p.acceptExpr1()
					if err != nil {
						return nil, err
					}
					n.rlist[1] = expr
					p.acceptBlanks()
				}
				if !p.accept(tokenSqClose) {
					return nil, p.errorf(
						"expected %s but got %s",
						tokenName(tokenSqClose),
						tokenName(p.peek().typ),
					)
				}
				left = node.NewPosNode(npos, n)
			} else {
				right, err := p.acceptExpr1()
				if err != nil {
					return nil, err
				}
				p.acceptBlanks()
				if p.accept(tokenColon) {
					n := &sliceNode{left, []expr{right, nil}}
					p.acceptBlanks()
					if p.peek().typ != tokenSqClose {
						expr, err := p.acceptExpr1()
						if err != nil {
							return nil, err
						}
						n.rlist[1] = expr
						p.acceptBlanks()
					}
					if !p.accept(tokenSqClose) {
						return nil, p.errorf(
							"expected %s but got %s",
							tokenName(tokenSqClose),
							tokenName(p.peek().typ),
						)
					}
					left = node.NewPosNode(npos, n)
				} else {
					n := &subscriptNode{left, right}
					p.acceptBlanks()
					if !p.accept(tokenSqClose) {
						return nil, p.errorf(
							"expected %s but got %s",
							tokenName(tokenSqClose),
							tokenName(p.peek().typ),
						)
					}
					left = node.NewPosNode(npos, n)
				}
			}
		} else if p.accept(tokenPOpen) {
			pos := p.token.pos
			n := &callNode{left, make([]expr, 0, 8)}
			p.acceptBlanks()
			if !p.accept(tokenPClose) {
				for {
					arg, err := p.acceptExpr1()
					if err != nil {
						return nil, err
					}
					n.rlist = append(n.rlist, arg)
					p.acceptBlanks()
					if p.accept(tokenComma) {
						p.acceptBlanks()
						if p.accept(tokenPClose) {
							break
						}
					} else if p.accept(tokenPClose) {
						break
					} else {
						return nil, p.errorf(
							"expected %s or %s but got %s",
							tokenName(tokenComma),
							tokenName(tokenPClose),
							tokenName(p.peek().typ),
						)
					}
				}
			}
			left = node.NewPosNode(pos, n)
		} else if p.accept(tokenDot) {
			dot := p.token
			p.acceptBlanks()
			if !p.acceptIdentifierLike() {
				return nil, p.errorf(
					"expected %s but got %s",
					tokenName(tokenIdentifier),
					tokenName(p.peek().typ),
				)
			}
			right := node.NewPosNode(p.token.pos, &identifierNode{p.token.val, false})
			left = node.NewPosNode(dot.pos, &dotNode{left, right})
		} else {
			break
		}
	}
	return left, nil
}

type literalNode interface {
	node.Node
	Value() string
}

type intNode struct {
	value string
}

// Clone clones itself.
func (n *intNode) Clone() node.Node {
	return &intNode{n.value}
}

func (n *intNode) TerminalNode() node.Node {
	return n
}

func (n *intNode) Position() *node.Pos {
	return nil
}

func (n *intNode) IsExpr() bool {
	return true
}

type floatNode struct {
	value string
}

// Clone clones itself.
func (n *floatNode) Clone() node.Node {
	return &floatNode{n.value}
}

func (n *floatNode) TerminalNode() node.Node {
	return n
}

func (n *floatNode) Position() *node.Pos {
	return nil
}

func (n *floatNode) IsExpr() bool {
	return true
}

type stringNode struct {
	value vainString
}

// Clone clones itself.
func (n *stringNode) Clone() node.Node {
	return &stringNode{n.value}
}

func (n *stringNode) TerminalNode() node.Node {
	return n
}

func (n *stringNode) Position() *node.Pos {
	return nil
}

func (n *stringNode) IsExpr() bool {
	return true
}

type listNode struct {
	value []expr
}

// Clone clones itself.
func (n *listNode) Clone() node.Node {
	value := make([]expr, len(n.value))
	for i := range n.value {
		value[i] = n.value[i].Clone()
	}
	return &listNode{value}
}

func (n *listNode) TerminalNode() node.Node {
	return n
}

func (n *listNode) Position() *node.Pos {
	return nil
}

func (n *listNode) IsExpr() bool {
	return true
}

type dictionaryNode struct {
	value [][]expr
}

// Clone clones itself.
func (n *dictionaryNode) Clone() node.Node {
	value := make([][]expr, len(n.value))
	for i := range n.value {
		kv := make([]expr, len(n.value[i]))
		for j := range n.value[i] {
			kv[j] = n.value[i][j].Clone()
		}
		value[i] = kv
	}
	return &dictionaryNode{value}
}

func (n *dictionaryNode) TerminalNode() node.Node {
	return n
}

func (n *dictionaryNode) Position() *node.Pos {
	return nil
}

func (n *dictionaryNode) IsExpr() bool {
	return true
}

type optionNode struct {
	value string
}

// Clone clones itself.
func (n *optionNode) Clone() node.Node {
	return &optionNode{n.value}
}

func (n *optionNode) TerminalNode() node.Node {
	return n
}

func (n *optionNode) Position() *node.Pos {
	return nil
}

func (n *optionNode) IsExpr() bool {
	return true
}

func (n *optionNode) Value() string {
	return n.value[1:]
}

type envNode struct {
	value string
}

// Clone clones itself.
func (n *envNode) Clone() node.Node {
	return &envNode{n.value}
}

func (n *envNode) TerminalNode() node.Node {
	return n
}

func (n *envNode) Position() *node.Pos {
	return nil
}

func (n *envNode) IsExpr() bool {
	return true
}

func (n *envNode) Value() string {
	return n.value[1:]
}

type regNode struct {
	value string
}

// Clone clones itself.
func (n *regNode) Clone() node.Node {
	return &regNode{n.value}
}

func (n *regNode) TerminalNode() node.Node {
	return n
}

func (n *regNode) Position() *node.Pos {
	return nil
}

func (n *regNode) IsExpr() bool {
	return true
}

func (n *regNode) Value() string {
	return n.value[1:]
}

// expr9: int /
//        float /
//        (string ABNF is too complex! e.g. "string\n", 'str''ing') /
//        "[" *blank *( expr1 *blank "," *blank ) "]" /
//        "{" *blank *( expr1 *blank ":" *blank expr1 *blank "," *blank ) "}" /
//        &option /
//        "(" *blank expr1 *blank ")" /
//        function /
//        identifier /
//        $VAR /
//        @r
func (p *parser) acceptExpr9() (expr, *node.ErrorNode) {
	if p.accept(tokenInt) {
		n := node.NewPosNode(p.token.pos, &intNode{p.token.val})
		return n, nil
	} else if p.accept(tokenFloat) {
		n := node.NewPosNode(p.token.pos, &floatNode{p.token.val})
		return n, nil
	} else if p.accept(tokenString) {
		n := node.NewPosNode(p.token.pos, &stringNode{vainString(p.token.val)})
		return n, nil
	} else if p.accept(tokenSqOpen) {
		n := &listNode{make([]expr, 0, 16)}
		p.acceptBlanks()
		if !p.accept(tokenSqClose) {
			for {
				expr, err := p.acceptExpr()
				if err != nil {
					return nil, err
				}
				n.value = append(n.value, expr)
				p.acceptBlanks()
				if p.accept(tokenComma) {
					p.acceptBlanks()
					if p.accept(tokenSqClose) {
						break
					}
				} else if p.accept(tokenSqClose) {
					break
				} else {
					return nil, p.errorf(
						"expected %s or %s but got %s",
						tokenName(tokenComma),
						tokenName(tokenSqClose),
						tokenName(p.peek().typ),
					)
				}
			}
		}
		return node.NewPosNode(p.token.pos, n), nil
	} else if p.accept(tokenCOpen) {
		npos := p.token.pos
		var m [][]expr
		p.acceptBlanks()
		if !p.accept(tokenCClose) {
			m = make([][]expr, 0, 16)
			for {
				left, err := p.acceptExpr()
				if err != nil {
					return nil, err
				}
				p.acceptBlanks()
				if !p.accept(tokenColon) {
					return nil, p.errorf(
						"expected %s but got %s",
						tokenName(tokenColon),
						tokenName(p.peek().typ),
					)
				}
				p.acceptBlanks()
				right, err := p.acceptExpr()
				if err != nil {
					return nil, err
				}
				m = append(m, []expr{left, right})
				p.acceptBlanks()
				if p.accept(tokenComma) {
					p.acceptBlanks()
					if p.accept(tokenSqClose) {
						break
					}
				} else if p.accept(tokenCClose) {
					break
				}
			}
		}
		n := node.NewPosNode(npos, &dictionaryNode{m})
		return n, nil
	} else if p.accept(tokenPOpen) {
		p.acceptBlanks()
		n, err := p.acceptExpr()
		if err != nil {
			return nil, err
		}
		p.acceptBlanks()
		if !p.accept(tokenPClose) {
			return nil, p.errorf(
				"expected %s but got %s", tokenName(tokenPClose), tokenName(p.peek().typ),
			)
		}
		return n, nil
	} else if p.accept(tokenFunc) {
		p.backup()
		return p.acceptFunction(true)
	} else if p.accept(tokenOption) {
		n := node.NewPosNode(p.token.pos, &optionNode{p.token.val})
		return n, nil
	} else if p.accept(tokenIdentifier) {
		n := node.NewPosNode(p.token.pos, &identifierNode{p.token.val, true})
		return n, nil
	} else if p.accept(tokenEnv) {
		n := node.NewPosNode(p.token.pos, &envNode{p.token.val})
		return n, nil
	} else if p.accept(tokenReg) {
		n := node.NewPosNode(p.token.pos, &regNode{p.token.val})
		return n, nil
	}
	return nil, p.errorf("expected expression but got %s", tokenName(p.peek().typ))
}
