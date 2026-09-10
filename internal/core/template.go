package core

// CategoryTemplate is a starter tree somebody can take in one click: a root
// category, the questions it asks, and optionally a level of children with
// questions of their own.
//
// ⚠ IT IS A STARTING POINT, NOT A SCHEMA. Everything it creates is ordinary data
// the operator owns from the moment it lands — renameable, deletable, and
// extendable exactly like a category they typed themselves. ADR-021 exists
// because a schema per trade shipped by us is the thing this product must not
// have; a template is the opposite of that, because nothing afterwards knows it
// came from one.
type CategoryTemplate struct {
	// Code is the root category's code, and the name this template is addressed
	// by. Applying a template whose code is already a root is refused rather
	// than merged: two trees under one address is not a state the tree can hold.
	Code string
	// Name is the root category's name, and what the operator reads on the
	// control that applies it.
	Name string
	// Summary is one line saying what taking it gets you.
	Summary string
	// Fields are asked by the root, so every child inherits them.
	Fields []CategoryField
	// Children is one level of subcategories. Empty when everything the
	// template asks is true of the whole trade, which is the shape a car part
	// has: the donor vehicle's specification describes a wing mirror exactly as
	// well as it describes a gearbox.
	Children []TemplateChild
}

// TemplateChild is one subcategory of a template, with the questions that are
// true of it and not of its siblings.
type TemplateChild struct {
	Code   string
	Name   string
	Fields []CategoryField
}

// Questions counts every question the template creates, across all its levels.
// It is what the operator is told before they press the button.
func (t CategoryTemplate) Questions() int {
	n := len(t.Fields)
	for _, c := range t.Children {
		n += len(c.Fields)
	}
	return n
}

// Nodes counts the categories the template creates, the root included.
func (t CategoryTemplate) Nodes() int { return 1 + len(t.Children) }
