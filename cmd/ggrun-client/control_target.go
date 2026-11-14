package main

import (
	"fmt"

	"github.com/raketenkater/ggrun/clientkit"
)

type controlTarget struct {
	alias    string
	context  int
	fallback bool
}

func selectedControlTarget(name string, p clientkit.Profile, goal bool) (controlTarget, error) {
	c, err := loadCustomization(name)
	if err != nil {
		return controlTarget{}, err
	}
	choice := c.ToolReviewer
	if goal {
		choice = c.GoalEvaluator
	}
	return controlTargetFor(p, choice)
}

func controlTargetFor(p clientkit.Profile, choice string) (controlTarget, error) {
	if choice == "" || choice == "main" {
		return controlTarget{alias: p.MainAlias, context: p.ContextPerSlot}, nil
	}
	if choice != "utility" {
		return controlTarget{}, fmt.Errorf("unsupported control model selection")
	}
	u := p.UtilityRoute
	if u == nil {
		return controlTarget{}, fmt.Errorf("server does not advertise utility context; update ggrun or select main for this evaluator")
	}
	if err := p.Validate(); err != nil {
		return controlTarget{}, err
	}
	return controlTarget{alias: p.UtilityAlias, context: u.ContextPerSlot, fallback: u.FallbackToMain}, nil
}
