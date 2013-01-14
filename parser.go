package main

import (
	"fmt"
	"io"
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
	lex   *Lexer
	token Token
	nodes []Node
}

func NewParser(name string, input io.Reader) *Parser {
	parse := &Parser{
		lex:   NewLexer(name, input),
		nodes: make([]Node, 0, 10),
	}

	tok, err := parse.lex.NextToken()

	if err != nil {
		panic(err)
	}

	parse.token = tok

	return parse
}

func (p *Parser) parseBlock() (*Node, error) {
	if _, err := p.expectType(tkOpenBrace); err != nil {
		return nil, err
	}

	// TODO: rest of block

	if _, err := p.expectType(tkCloseBrace); err != nil {
		return nil, err
	}

	block := BlockNode{}

	var node Node = block

	return &node, nil
}

// zero or more comma separated variables
func (p *Parser) parseVariableList() ([]string, error) {

	var vars []string = nil

	id, err := p.acceptType(tkIdent)
	for id != nil && err == nil {
		vars = append(vars, id.value)

		if tok, err := p.acceptType(tkComma); tok == nil || err != nil {
			break
		}

		if id, err = p.expectType(tkIdent); err != nil {
			return nil, err
		}
	}

	return vars, nil
}

func (p *Parser) parseExternVarDecl() (*Node, error) {
	_, err := p.expect(tkKeyword, "extrn")

	if err != nil {
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
		return nil, NewParseError(p.token,
			"expected at least 1 variable in extrn declaration")
	}

	var node Node = varNode
	return &node, nil
}

func (p *Parser) parseFuncDeclaration() (*Node, error) {
	var err error

	id, err := p.expectType(tkIdent)

	if err != nil {
		return nil, err
	}

	fnNode := FunctionNode{name: id.value}

	if _, err = p.expectType(tkOpenParen); err != nil {
		return nil, err
	}

	if fnNode.params, err = p.parseVariableList(); err != nil {
		return nil, err
	}

	if _, err = p.expectType(tkCloseParen); err != nil {
		return nil, err
	}

	var block *Node

	if block, err = p.parseBlock(); block == nil || err != nil {
		return nil, err
	}

	fnNode.block = (*block).(BlockNode)

	var node Node = fnNode
	return &node, err
}

func (p *Parser) parseExternalVariableInit() (*Node, error) {
	var err error
	var tok *Token

	ident, err := p.expectType(tkIdent)

	if err != nil {
		return nil, err
	}

	retNode := ExternVarInitNode{name: ident.value}

	if tok, err = p.acceptType(tkNumber); tok != nil {
		retNode.value = IntegerNode{tok.value}
	} else if tok, err = p.acceptType(tkCharacter); tok != nil {
		retNode.value = CharacterNode{tok.value}
	} else {
		return nil, NewParseError(p.token, "expected value type")
	}

	if err != nil {
		return nil, err
	}

	tok, err = p.expectType(tkSemicolon)

	var node Node = retNode
	return &node, err
}

// function declaration or external variable
func (p *Parser) parseTopLevel() (*Node, error) {
	if node, err := p.parseFuncDeclaration(); node != nil {
		return node, err
	} else if node, err := p.parseExternalVariableInit(); node != nil {
		return node, err
	}

	return nil, NewParseError(p.token, "expected top level decl")
}

func (p *Parser) Parse() error {
	tok, _ := p.lex.NextToken()
	return NewParseError(tok, "Parser not implemented")
}

func (p *Parser) nextToken() (Token, error) {
	tok, err := p.lex.NextToken()

	if err != nil {
		return tok, err
	}

	p.token = tok
	return tok, nil
}

func (p *Parser) accept(t TokenType, str string) (*Token, error) {
	var tok Token
	var err error = nil

	if p.token.kind == t {
		if str == "" || str == p.token.value {
			tok = p.token

			// Get next token if we've matched
			next, err := p.lex.NextToken()
			p.token = next
			return &tok, err

		}
	}

	return nil, err
}

func (p *Parser) acceptType(t TokenType) (*Token, error) {
	return p.accept(t, "")
}

func (p *Parser) expectType(t TokenType) (*Token, error) {
	return p.expect(t, "")
}

func (p *Parser) expect(t TokenType, str string) (*Token, error) {
	tok, err := p.accept(t, str)

	if tok == nil {
		if str == "" {
			return nil, NewParseError(p.token,
				fmt.Sprintf("Expected %v", t))
		} else {
			return nil, NewParseError(p.token,
				fmt.Sprintf("Expected (%v: %v)", t, str))
		}
	}

	if err != nil {
		return nil, err
	}

	return tok, nil
}
