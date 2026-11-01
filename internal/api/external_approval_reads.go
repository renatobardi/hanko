package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/service"
)

// Leitura da aprovação externa (ADR-0081; fatia 3 da v0.8, #127). O Hanko só observa: nenhuma operação aprova, recusa nem roda
// algo no host. Qualquer membro vê o pedido sem o script; o script é de owner e admin, com `activity_event`.

func (s *server) registerApprovalReads(api huma.API) {
	const tag = "external-rounds"
	b := "/companies/{company}"
	huma.Register(api, op("list-external-approvals", http.MethodGet, b+"/external-approvals", tag,
		"Lista os pedidos do canal de aprovação observados nos hosts, sem o script"), s.listExternalApprovals)
	huma.Register(api, op("get-external-approval", http.MethodGet, b+"/external-approvals/{approval}", tag,
		"Mostra um pedido do canal, com o sha256 e as versões; o comando oute approve <id> vai para copiar"), s.getExternalApproval)
	huma.Register(api, op("get-approval-script", http.MethodGet, b+"/external-approvals/{approval}/script", tag,
		"Mostra o corpo do script de um pedido (owner ou admin; cada leitura gera activity_event)"), s.getApprovalScript)
}

func approvalOut(v service.ApprovalView) ExternalApproval {
	return ExternalApproval{ID: v.ID.String(), CanalID: v.CanalID, Title: v.Title, RequestedAs: v.RequestedAs, HostName: v.HostName, HostID: idPtrStr(v.HostID),
		AgentID: idPtrStr(v.AgentID), State: v.State, Decision: v.Decision, Approver: v.Approver, ApprovedBy: v.ApprovedBy, RC: v.RC, DurationS: v.DurationS,
		OutputBytes: v.OutputBytes, ScriptSHA256: v.ScriptSHA256, DecidedSHA256: v.DecidedSHA256, Versions: v.Versions, RequestedAt: v.RequestedAt,
		DecidedAt: v.DecidedAt, ListedAt: v.ListedAt}
}

func (s *server) listExternalApprovals(ctx context.Context, in *listExternalApprovalsIn) (*ListOut[ExternalApproval], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListExternalApprovals(ctx, sc.Company.ID, in.State, in.Limit, in.Offset)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]ExternalApproval, len(rows))
	for i, r := range rows {
		items[i] = approvalOut(r)
	}
	return &ListOut[ExternalApproval]{Body: ListBody[ExternalApproval]{Items: items, Limit: in.Limit, Offset: in.Offset}}, nil
}

func (s *server) getExternalApproval(ctx context.Context, in *ExternalApprovalPath) (*ItemOut[ExternalApproval], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("approval", in.Approval)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.GetExternalApproval(ctx, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalApproval]{Body: approvalOut(v)}, nil
}

func (s *server) getApprovalScript(ctx context.Context, in *ExternalApprovalPath) (*ItemOut[ApprovalScript], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("approval", in.Approval)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.ApprovalScript(ctx, sc.Actor, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	versions := make([]ApprovalVersion, len(v.Versions))
	for i, x := range v.Versions {
		versions[i] = ApprovalVersion{SHA256: x.SHA256, SeenAt: x.SeenAt}
	}
	return &ItemOut[ApprovalScript]{Body: ApprovalScript{ApprovalID: v.ApprovalID.String(), SHA256: v.SHA256, Script: v.Script, Versions: versions}}, nil
}
