package parse

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

type ParseError struct {
	tok Token
	msg string
}

func (p *ParseError) Error() string {
	return fmt.Sprintf("Parse error on line %d, at token: %s: %s",
		p.tok.start.Line, p.tok.String(), p.msg)
}

func NewParseError(tok Token, msg string) error {
	return &ParseError{tok, msg}
}

type Parser struct {
	lex    *Lexer
	tokens []Token
	tokIdx int
	nodes  []Node
}

func NewParser(name string, input io.Reader) *Parser {
	parse := &Parser{
		lex:    NewLexer(name, input),
		nodes:  make([]Node, 0, 10),
		tokens: make([]Token, 0, 10),
		tokIdx: -1,
	}

	if _, err := parse.nextToken(); err != nil {
		panic(err)
	}

	return parse
}

func (p *Parser) Parse() (unit TranslationUnit, err error) {
	var node *Node = nil
	unit = TranslationUnit{File: p.lex.name}

	// Bail out of lex errors
	// TODO: this is sort of convoluted logic, refactor
	defer func() {
		if e := recover(); e != nil {
			// if it's a lex error, trap, return
			if lexErr, ok := e.(*LexError); ok {
				unit, err = TranslationUnit{}, lexErr
			} else {
				// rethrow
				panic(e)
			}
		}
	}()

	for {
		if _, ok := p.acceptType(tkEof); ok {
			break
		}

		if node, err = p.parseTopLevel(); err != nil {
			return unit, err
		}

		switch (*node).(type) {
		case FunctionNode:
			unit.Funcs = append(unit.Funcs, (*node).(FunctionNode))
		case ExternVarInitNode, ExternVecInitNode:
			unit.Vars = append(unit.Vars, *node)
		default:
			return unit, NewParseError(p.token(),
				"That's not a top level decl")
		}
	}

	return unit, nil
}

func (p *Parser) accept(t TokenType, str string) (*Token, bool) {
	var tok Token

	if p.token().kind == t {
		if str == "" || str == p.token().value {
			tok = p.token()

			// Get next token if we've matched
			if _, err := p.nextToken(); err != nil {
				// TODO: handle this
				panic(err)
			}

			return &tok, true

		}
	}

	return nil, false
}

func (p *Parser) acceptType(t TokenType) (*Token, bool) {
	return p.accept(t, "")
}

func (p *Parser) expect(t TokenType, str string) (*Token, error) {
	tok, ok := p.accept(t, str)
	if !ok {
		if str == "" {
			return nil, NewParseError(p.token(),
				fmt.Sprintf("Expected %v", t))
		} else {
			return nil, NewParseError(p.token(),
				fmt.Sprintf("Expected (%v: %v)", t, str))
		}
	}

	return tok, nil
}

func (p *Parser) expectOneOf(t ...TokenType) (TokenType, Token, error) {
	tok := p.token()

	for _, tt := range t {
		if p.token().kind == tt {
			p.nextToken()
			return tt, tok, nil
		}
	}

	types := make([]string, len(t), len(t))

	for i, tt := range t {
		types[i] = fmt.Sprintf("%s", tt)
	}

	return tkError, (&tok).Error(), NewParseError(p.token(),
		fmt.Sprintf("Expected one of: %s", strings.Join(types, ", ")))
}

func (p *Parser) expectType(t TokenType) (*Token, error) {
	return p.expect(t, "")
}

func (p *Parser) nextToken() (Token, error) {
	p.tokIdx += 1

	if p.tokIdx < len(p.tokens) {
		return p.tokens[p.tokIdx], nil
	}

	tok, err := p.lex.NextToken()
	if err != nil {
		return tok, err
	}

	p.tokens = append(p.tokens, tok)

	return tok, nil
}

func (p *Parser) parseBlock() (*Node, error) {
	if _, err := p.expectType(tkOpenBrace); err != nil {
		return nil, err
	}

	block := BlockNode{}

	for p.token().kind != tkCloseBrace {
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}

		block.Nodes = append(block.Nodes, *stmt)
	}

	if _, err := p.expectType(tkCloseBrace); err != nil {
		return nil, err
	}

	var node Node = block
	return &node, nil
}

func (p *Parser) parseConstant() (*Node, error) {
	var node Node

	kind, tok, err := p.expectOneOf(tkNumber, tkCharacter, tkString)

	if err != nil {
		return nil, err
	}

	switch kind {
	case tkNumber:
		num, err := strconv.Atoi(tok.value)
		if err != nil {
			return nil, NewParseError(p.token(), "invalid integer literal")
		}

		node = IntegerNode{num}
		return &node, err
	case tkCharacter:
		node = CharacterNode{tok.value}
		return &node, err
	case tkString:
		node = StringNode{tok.value}
		return &node, err
	default:
		return nil, err
	}

	return nil, nil
}

func (p *Parser) parseSubExpression() (*Node, error) {
	unNode := UnaryNode{Oper: ""}

	// Unary prefix operator
	if tok, ok := p.acceptType(tkOperator); ok {
		// *, &, -, !, ++, --, and ~.
		switch tok.value {
		case "*", "&", "-", "!", "++", "--", "~":
			unNode = UnaryNode{Oper: tok.value, Postfix: false}
		default:
			return nil, NewParseError(p.token(), "invalid unary op")
		}
	}

	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	// TODO: this logic is ugly.
	if unNode.Oper != "" {
		unNode.Node = *expr
		*expr = unNode
	}

	if p.token().kind == tkOperator {
		switch p.token().value {
		case "++", "--": // Unary postfix operator
			unNode = UnaryNode{Oper: p.token().value,
				Node: *expr, Postfix: true}
			*expr = unNode

			p.nextToken()
		}
	}

	return expr, nil
}

func (p *Parser) parseExpression() (*Node, error) {
	node, err := p.parseSubExpression()
	if err != nil {
		return nil, err
	}

	if tok, ok := p.acceptType(tkOperator); ok {
		rhs, err := p.parseExpression()
		if err != nil {
			return nil, err
		}

		var bin BinaryNode

		// Resolve precedence for multiple operators in expression
		// TODO: currently ignores LTR, RTL binding
		if rbin, ok := (*rhs).(BinaryNode); ok {
			lproc, _ := OperatorPrecedence(tok.value)
			rproc, _ := OperatorPrecedence(rbin.Oper)

			if lproc > rproc {
				left := BinaryNode{Left: *node, Oper: tok.value,
					Right: rbin.Left}
				bin = BinaryNode{Left: left, Oper: rbin.Oper,
					Right: rbin.Right}
			} else {
				bin = BinaryNode{Left: *node, Oper: tok.value,
					Right: rbin}
			}

		} else {
			bin = BinaryNode{Left: *node,
				Oper: tok.value, Right: *rhs}
		}

		*node = bin
	}

	// Ternary operator
	if _, ok := p.acceptType(tkTernary); ok {
		ter := TernaryNode{Cond: *node}

		if body, err := p.parseExpression(); err != nil {
			return nil, err
		} else {
			ter.TrueBody = *body
		}

		if _, err := p.expectType(tkColon); err != nil {
			return nil, err
		}

		if body, err := p.parseExpression(); err != nil {
			return nil, err
		} else {
			ter.FalseBody = *body
		}

		*node = ter
	}

	return node, nil
}

func (p *Parser) parseExternVarDecl() (*Node, error) {
	var err error

	if _, err = p.expect(tkKeyword, "extrn"); err != nil {
		return nil, err
	}

	varNode := ExternVarDeclNode{}

	if varNode.names, err = p.parseVariableList(); err != nil {
		return nil, err
	}

	if _, err = p.expectType(tkSemicolon); err != nil {
		return nil, err
	}

	if len(varNode.names) <= 0 {
		return nil, NewParseError(p.token(),
			"expected at least 1 variable in extrn"+
				" declaration")
	}

	var node Node = varNode
	return &node, nil
}

func (p *Parser) parseExternalVariableInit() (*Node, error) {
	var err error

	ident, err := p.expectType(tkIdent)

	if err != nil {
		return nil, err
	}

	if _, ok := p.acceptType(tkOpenBracket); ok {
		init := ExternVecInitNode{Name: ident.value}

		size, err := p.expectType(tkNumber)
		if err != nil {
			return nil, err
		}
		if _, err := p.expectType(tkCloseBracket); err != nil {
			return nil, err
		}

		// TODO: Assert declared size == actual size

		init.Size, err = strconv.Atoi(size.value)

		if err != nil {
			return nil, NewParseError(p.token(),
				"Bad integer literal")
		}

		for {
			if constant, err := p.parseConstant(); err != nil {
				return nil, err
			} else {
				init.Values = append(init.Values, *constant)
			}

			if _, ok := p.acceptType(tkComma); !ok {
				break
			}
		}

		var node Node = init
		if _, err = p.expectType(tkSemicolon); err != nil {
			return nil, err
		}
		return &node, nil
	} else {
		init := ExternVarInitNode{Name: ident.value}

		constant, err := p.parseConstant()
		if err != nil {
			if _, err = p.expectType(tkSemicolon); err == nil {
				// Empty declarations are zero filled
				init.Value = IntegerNode{0}
				var node Node = init
				return &node, nil
			}
		} else {
			init.Value = *constant
		}

		if err != nil {
			return nil, err
		}

		var node Node = init
		if _, err = p.expectType(tkSemicolon); err != nil {
			return nil, err
		}
		return &node, nil
	}

	return nil, nil
}

func (p *Parser) parseFuncDeclaration() (*Node, error) {
	var err error

	id, err := p.expectType(tkIdent)

	if err != nil {
		return nil, err
	}

	fnNode := FunctionNode{Name: id.value}

	if _, err = p.expectType(tkOpenParen); err != nil {
		return nil, err
	}

	if fnNode.Params, err = p.parseVariableList(); err != nil {
		return nil, err
	}

	if _, err = p.expectType(tkCloseParen); err != nil {
		return nil, err
	}

	var stmt *Node

	if stmt, err = p.parseStatement(); stmt == nil || err != nil {
		return nil, err
	}

	fnNode.Body = *stmt

	var node Node = fnNode
	return &node, err
}

func (p *Parser) parseIdent() (*Node, error) {
	tok, err := p.expectType(tkIdent)

	if err != nil {
		return nil, err
	}

	var node Node = IdentNode{tok.value}
	return &node, nil
}

func (p *Parser) parseIf() (*Node, error) {
	if _, err := p.expect(tkKeyword, "if"); err != nil {
		return nil, err
	}

	if _, err := p.expectType(tkOpenParen); err != nil {
		return nil, err
	}

	cond, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	if _, err := p.expectType(tkCloseParen); err != nil {
		return nil, err
	}

	trueBody, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	var elseBody Node
	var hasElse = false

	if _, ok := p.accept(tkKeyword, "else"); ok {
		hasElse = true
		els, err := p.parseStatement()
		if err != nil {
			return nil, err
		}

		elseBody = *els
	}

	var node Node = IfNode{Cond: *cond, Body: *trueBody, HasElse: hasElse,
		ElseBody: elseBody}
	return &node, nil

}

func (p *Parser) parseParen() (*Node, error) {
	if _, err := p.expectType(tkOpenParen); err != nil {
		return nil, err
	}

	inner, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	if _, err := p.expectType(tkCloseParen); err != nil {
		return nil, err
	}

	var node Node = ParenNode{*inner}
	return &node, nil
}

// TODO: unfinished, untested
func (p *Parser) parsePrimary() (node *Node, err error) {
	if node, err = p.parseParen(); err == nil {
	} else if node, err = p.parseConstant(); err == nil {
	} else if node, err = p.parseIdent(); err == nil {
	} else {
		return nil, NewParseError(p.token(), "expected primary expression")
	}

	// Array access
	if _, ok := p.acceptType(tkOpenBracket); ok {
		array := *node
		index, err := p.parseExpression()

		if err != nil {
			return nil, err
		}
		if _, err := p.expectType(tkCloseBracket); err != nil {
			return nil, err
		}

		*node = ArrayAccessNode{Array: array, Index: *index}
		return node, nil
	}

	// Function call
	if _, ok := p.acceptType(tkOpenParen); ok {
		args := make([]Node, 0, 10)

		if p.token().kind != tkCloseParen {
			for {
				arg, err := p.parseExpression()

				if err != nil {
					return nil, err
				}
				args = append(args, *arg)

				if _, ok := p.acceptType(tkComma); !ok {
					break
				}
			}
		}

		if _, err := p.expectType(tkCloseParen); err != nil {
			return nil, err
		}
		*node = FunctionCallNode{Callable: *node, Args: args}
		return node, nil
	}

	return node, nil
}

func (p *Parser) parseStatement() (node *Node, err error) {
	pos := p.tokIdx

	if node, err := p.parseIf(); err != nil && p.tokIdx != pos {
		return nil, err
	} else if err == nil {
		return node, nil
	}

	if node, err := p.parseBlock(); err != nil && p.tokIdx != pos {
		return nil, err
	} else if err == nil {
		return node, nil
	}

	if node, err := p.parseVarDecl(); err != nil && p.tokIdx != pos {
		return nil, err
	} else if err == nil {
		return node, nil
	}

	if node, err := p.parseExternVarDecl(); err != nil && p.tokIdx != pos {
		return nil, err
	} else if err == nil {
		return node, nil
	}

	if node, err := p.parseWhile(); err != nil && p.tokIdx != pos {
		return nil, err
	} else if err == nil {
		return node, nil
	}

	if node, err := p.parseSwitch(); err != nil && p.tokIdx != pos {
		return nil, err
	} else if err == nil {
		return node, nil
	}

	if _, ok := p.acceptType(tkSemicolon); ok {
		var null Node = NullNode{}
		return &null, nil
	}

	if _, ok := p.accept(tkKeyword, "break"); ok {
		if _, err := p.expectType(tkSemicolon); err != nil {
			return nil, err
		}

		var brk Node = BreakNode{}
		return &brk, nil
	}

	if _, ok := p.accept(tkKeyword, "return"); ok {
		var retNode ReturnNode
		if _, ok := p.acceptType(tkSemicolon); ok {
			retNode.Node = NullNode{}
		} else {
			node, err := p.parseExpression()
			if err != nil {
				return nil, err
			}

			if _, err := p.expectType(tkSemicolon); err != nil {
				return nil, err
			}
			retNode.Node = *node
		}

		var node Node = retNode
		return &node, nil
	}

	if _, ok := p.accept(tkKeyword, "goto"); ok {
		var tok *Token = nil

		if tok, err = p.expectType(tkIdent); err != nil {
			return nil, err
		}

		var gt Node = GotoNode{Label: tok.value}

		if _, err := p.expectType(tkSemicolon); err != nil {
			return nil, err
		}

		return &gt, nil
	}

	if tok, ok := p.acceptType(tkIdent); ok {
		if _, ok := p.acceptType(tkColon); ok {
			var node Node = LabelNode{tok.value}
			return &node, nil
		} else if _, ok := p.acceptType(tkSemicolon); ok {
			var node Node = StatementNode{IdentNode{tok.value}}
			return &node, nil
		}

		// rewind
		p.tokIdx = pos
	}

	if node, err := p.parseExpression(); err != nil && p.tokIdx != pos {
		return nil, err
	} else if err == nil {
		if _, err := p.expectType(tkSemicolon); err != nil {
			return nil, err
		}
		*node = StatementNode{Expr: *node}
		return node, nil
	}

	return nil, NewParseError(p.tokenAt(pos), "expected statement")
}

// TODO: this logic is all over the place. refactor.
func (p *Parser) parseSwitch() (*Node, error) {
	var switchNode SwitchNode

	if _, err := p.expect(tkKeyword, "switch"); err != nil {
		return nil, err
	}

	if _, err := p.expectType(tkOpenParen); err != nil {
		return nil, err
	}

	if cond, err := p.parseExpression(); err != nil {
		return nil, err
	} else {
		switchNode.Cond = *cond
	}

	if _, err := p.expectType(tkCloseParen); err != nil {
		return nil, err
	}

	// I know, it can technically be any statement, but I'll leave it
	// as a block for now.
	if _, err := p.expectType(tkOpenBrace); err != nil {
		return nil, err
	}

	for {
		if _, ok := p.acceptType(tkCloseBrace); ok {
			break
		}

		if _, ok := p.accept(tkKeyword, "case"); ok {
			var c CaseNode

			if cond, err := p.parseConstant(); err != nil {
				return nil, err
			} else {
				c = CaseNode{Cond: *cond}
			}

			if _, err := p.expectType(tkColon); err != nil {
				return nil, err
			}

			for {
				if _, ok := p.accept(tkKeyword, "case"); ok {
					p.tokIdx -= 1
					break
				} else if _, ok := p.accept(tkKeyword, "default"); ok {
					p.tokIdx -= 1
					break
				} else if _, ok := p.acceptType(tkCloseBrace); ok {
					p.tokIdx -= 1
					break
				}

				if stmt, err := p.parseStatement(); err != nil {
					return nil, err
				} else {
					c.Statements = append(c.Statements, *stmt)
				}
			}

			switchNode.Cases = append(switchNode.Cases, c)

		} else if _, ok := p.accept(tkKeyword, "default"); ok {
			if _, err := p.expectType(tkColon); err != nil {
				return nil, err
			}

			if switchNode.DefaultCase != nil {
				return nil, NewParseError(p.token(),
					"Multiple 'default' cases")
			}

			for {
				if _, ok := p.accept(tkKeyword, "case"); ok {
					p.tokIdx -= 1
					break
				} else if _, ok := p.accept(tkKeyword, "default"); ok {
					p.tokIdx -= 1
					break
				} else if _, ok := p.acceptType(tkCloseBrace); ok {
					p.tokIdx -= 1
					break
				}

				if stmt, err := p.parseStatement(); err != nil {
					return nil, err
				} else {
					switchNode.DefaultCase =
						append(switchNode.DefaultCase, *stmt)
				}
			}

		} else {
			return nil, NewParseError(p.token(),
				"expected 'case' or 'default'")
		}
	}

	var node Node = switchNode
	return &node, nil
}

// function declaration or external variable
func (p *Parser) parseTopLevel() (node *Node, err error) {
	pos := p.tokIdx

	// FIXME: this is pretty convoluted logic.

	if node, err := p.parseExternalVariableInit(); err == nil {
		return node, nil
	} else if p.tokIdx == pos+1 {
		// Rewind to previous position if only ident is encountered
		p.tokIdx = pos
	} else {
		// Otherwise, it's an actual syntax error
		return nil, err
	}

	if node, err := p.parseFuncDeclaration(); err == nil {
		return node, nil
	} else if p.tokIdx != pos {
		return nil, err
	}

	return nil, NewParseError(p.token(), "expected top level decl")
}

func (p *Parser) parseVarDecl() (*Node, error) {
	var err error

	if _, err = p.expect(tkKeyword, "auto"); err != nil {
		return nil, err
	}

	varNode := VarDeclNode{}

	for {
		ident, err := p.expectType(tkIdent)
		if err != nil {
			return nil, err
		}

		if _, ok := p.acceptType(tkOpenBracket); ok {

			if num, err := p.expectType(tkNumber); err != nil {
				return nil, err
			} else {
				size, err := strconv.Atoi(num.value)

				if err != nil {
					return nil, NewParseError(p.token(), "invalid integer literal")
				}

				varNode.Vars = append(varNode.Vars,
					VarDecl{ident.value, true, size})
			}

			if _, err := p.expectType(tkCloseBracket); err != nil {
				return nil, err
			}
		} else {
			varNode.Vars = append(varNode.Vars,
				VarDecl{ident.value, false, 0})
		}

		if _, ok := p.acceptType(tkComma); !ok {
			break
		}
	}

	if _, err = p.expectType(tkSemicolon); err != nil {
		return nil, err
	}

	if len(varNode.Vars) <= 0 {
		return nil, NewParseError(p.token(),
			"expected at least 1 variable in auto declaration")
	}

	var node Node = varNode
	return &node, nil
}

// zero or more comma separated variables
func (p *Parser) parseVariableList() ([]string, error) {
	var err error
	var vars []string = nil

	id, ok := p.acceptType(tkIdent)
	for id != nil && ok {
		vars = append(vars, id.value)

		if _, ok := p.acceptType(tkComma); !ok {
			break
		}

		if id, err = p.expectType(tkIdent); err != nil {
			return nil, err
		}
	}

	return vars, nil
}

func (p *Parser) parseWhile() (*Node, error) {
	if _, err := p.expect(tkKeyword, "while"); err != nil {
		return nil, err
	}

	if _, err := p.expectType(tkOpenParen); err != nil {
		return nil, err
	}

	cond, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	if _, err := p.expectType(tkCloseParen); err != nil {
		return nil, err
	}

	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	var node Node = WhileNode{Cond: *cond, Body: *body}
	return &node, nil
}

func (p *Parser) tokenAt(idx int) Token { return p.tokens[idx] }
func (p *Parser) token() Token          { return p.tokenAt(p.tokIdx) }
