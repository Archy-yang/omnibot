package tools

// misc_tools.go — 基础工具:时间与计算器(自 agent 包 builtin_tools.go 迁入)。

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"time"

	"omnibot/internal/pkg/toolcore"
)

func CreateGetCurrentTimeTool() toolcore.Tool {
	return toolcore.Tool{
		Name:         "get_current_time",
		Description:  "获取当前的日期和时间",
		DisplayLabel: "查询了当前时间",
		Capabilities: []string{toolcore.CapBasic},
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return time.Now().Format("2006-01-02 15:04:05 MST"), nil
		},
	}
}

// CreateCalculatorTool 计算器工具
func CreateCalculatorTool() toolcore.Tool {
	return toolcore.Tool{
		Name:         "calculator",
		Description:  "执行安全的数学计算（仅支持四则运算和括号）",
		DisplayLabel: "计算了一下",
		Capabilities: []string{toolcore.CapBasic},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"expression": map[string]interface{}{
					"type":        "string",
					"description": "数学表达式，如 \"2 + 3 * 4\"",
				},
			},
			"required": []string{"expression"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			expr, ok := args["expression"].(string)
			if !ok || expr == "" {
				return "", fmt.Errorf("expression is required")
			}
			result, err := safeEval(expr)
			if err != nil {
				return "", fmt.Errorf("计算失败: %w", err)
			}
			return strconv.FormatFloat(result, 'f', -1, 64), nil
		},
	}
}

// CreateSearchMemoriesTool 搜索记忆工具。
// 服务实现 MemorySearcher 时走语义检索(含来源标识);否则老子串路径兜底(12-记忆系统技术方案 §8)。

func safeEval(expr string) (float64, error) {
	for _, ch := range expr {
		if !strings.ContainsRune("0123456789+-*/(). ", ch) {
			return 0, fmt.Errorf("表达式包含不允许的字符: %c", ch)
		}
	}
	node, err := parser.ParseExpr(expr)
	if err != nil {
		return 0, err
	}
	return evalNode(node)
}

func evalNode(node ast.Expr) (float64, error) {
	switch n := node.(type) {
	case *ast.BinaryExpr:
		left, err := evalNode(n.X)
		if err != nil {
			return 0, err
		}
		right, err := evalNode(n.Y)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		default:
			return 0, fmt.Errorf("unsupported operator: %s", n.Op)
		}
	case *ast.ParenExpr:
		return evalNode(n.X)
	case *ast.BasicLit:
		if n.Kind == token.INT || n.Kind == token.FLOAT {
			return strconv.ParseFloat(n.Value, 64)
		}
		return 0, fmt.Errorf("unsupported literal: %s", n.Value)
	case *ast.UnaryExpr:
		val, err := evalNode(n.X)
		if err != nil {
			return 0, err
		}
		if n.Op == token.SUB {
			return -val, nil
		}
		return val, nil
	default:
		return 0, fmt.Errorf("unsupported expression type: %T", node)
	}
}
