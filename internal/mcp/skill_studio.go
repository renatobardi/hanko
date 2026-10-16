package mcp

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Estúdio de skills (ADR-0035): a run de teste de uma skill só enxerga aquela skill, na versão do teste, e não age
// na empresa. hanko_skill_get/list respondem com a skill testada, e as demais tools hanko_* são recusadas.

// skillTestBackend resolve o teste de uma task. Opcional: sem ele, nenhuma run é tratada como teste.
type skillTestBackend interface {
	SkillTestRunSpec(ctx context.Context, companyID, taskID domain.ID) (service.SkillTestSpec, error)
}

// skillTestAllowed são as tools que uma run de teste pode chamar.
var skillTestAllowed = map[string]bool{"hanko_whoami": true, "hanko_skill_list": true, "hanko_skill_get": true}

// testSpec devolve o teste da run do token, quando a run é de teste.
func testSpec(ctx context.Context, b any) (service.SkillTestSpec, bool, error) {
	id, ok := identityFrom(ctx)
	if !ok || !id.ViaToken || id.TaskID == nil || id.CompanyID == nil {
		return service.SkillTestSpec{}, false, nil
	}
	tb, ok := b.(skillTestBackend)
	if !ok {
		return service.SkillTestSpec{}, false, nil
	}
	spec, err := tb.SkillTestRunSpec(ctx, *id.CompanyID, *id.TaskID)
	if errors.Is(err, domain.ErrNotFound) {
		return service.SkillTestSpec{}, false, nil
	}
	if err != nil {
		return service.SkillTestSpec{}, false, fmt.Errorf("teste de skill: %w", err)
	}
	return spec, true, nil
}

// skillTestGuard recusa, numa run de teste do estúdio, toda tool fora de skillTestAllowed.
func skillTestGuard(b any) sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			ctr, ok := req.(*sdk.CallToolRequest)
			if !ok || ctr.Params == nil || skillTestAllowed[ctr.Params.Name] {
				return next(ctx, method, req)
			}
			_, isTest, err := testSpec(ctx, b)
			if err != nil {
				return nil, err
			}
			if isTest {
				msg := fmt.Sprintf("esta é uma execução de teste de skill: a tool %s não está disponível (só hanko_skill_get e hanko_skill_list)", ctr.Params.Name)
				return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: msg}}}, nil
			}
			return next(ctx, method, req)
		}
	}
}
