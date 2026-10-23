package service

import (
	"context"
	"fmt"
	"sync"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/policy"
)

// PolicyService 实现命令策略管理（对应需求 FR-03 / FR-04）。
//
// 策略引擎（shared/policy）被管理服务（配置）与服务端（执行）共用，
// 保证两侧判定逻辑完全一致。
type PolicyService struct {
	policyRepo *repo.PolicyRepo
	configRepo *repo.ConfigRepo
	oplog      *repo.OperationLogRepo

	mu     sync.RWMutex
	engine *policy.Engine // 缓存引擎，策略变更时失效重建
}

// NewPolicyService 创建策略服务。
func NewPolicyService(policyRepo *repo.PolicyRepo, configRepo *repo.ConfigRepo, oplog *repo.OperationLogRepo) *PolicyService {
	return &PolicyService{policyRepo: policyRepo, configRepo: configRepo, oplog: oplog}
}

// CommandPolicyRequest 是策略创建/更新请求。
type CommandPolicyRequest struct {
	PolicyName     string        `json:"policy_name"`
	PolicyType     string        `json:"policy_type"`
	MatchType      string        `json:"match_type"`
	Pattern        string        `json:"pattern"`
	RiskLevel      string        `json:"risk_level"`
	Action         string        `json:"action"`
	Scope          *policy.Scope `json:"scope"`
	Priority       int           `json:"priority"`
	Enabled        *bool         `json:"enabled"`
	TimeoutSeconds int           `json:"timeout_seconds"`
	Remark         string        `json:"remark"`
}

// validate 校验策略参数（创建场景：名称与匹配模式必填）。
func (r *CommandPolicyRequest) validate() error {
	if r.PolicyName == "" {
		return fmt.Errorf("策略名称不能为空")
	}
	if r.Pattern == "" {
		return fmt.Errorf("匹配模式不能为空")
	}
	return r.validateRule()
}

// validateRule 校验匹配模式与处置动作（更新场景：允许部分字段）。
//
// 更新接口是"部分更新"语义，调用方可能只改处置动作；
// 此时不能因为没带策略名称就报错，否则内置策略的动作无法调整。
func (r *CommandPolicyRequest) validateRule() error {
	if r.Pattern != "" {
		// 校验正则合法性（提前发现，避免运行期才报错）
		testRule := &policy.Rule{
			Name: r.PolicyName, MatchType: r.MatchType, Pattern: r.Pattern,
			Action: r.Action, RiskLevel: r.RiskLevel, Priority: r.Priority,
		}
		if _, err := policy.NewEngine([]*policy.Rule{testRule}, policy.ActionAllow); err != nil {
			return err
		}
	}
	if r.Action != "" {
		switch r.Action {
		case policy.ActionAllow, policy.ActionAudit, policy.ActionConfirm, policy.ActionDeny:
		default:
			return fmt.Errorf("处置动作非法：%s（可选 allow / audit / confirm / deny）", r.Action)
		}
	}
	if r.MatchType != "" {
		switch r.MatchType {
		case policy.MatchRegex, policy.MatchKeyword, policy.MatchPrefix, policy.MatchExact, policy.MatchCommand:
		default:
			return fmt.Errorf("匹配方式非法：%s（可选 regex / keyword / prefix / exact / command）", r.MatchType)
		}
	}
	return nil
}

// ListPolicies 查询策略列表。
func (s *PolicyService) ListPolicies(ctx context.Context, f repo.PolicyFilter) ([]*model.CommandPolicy, int64, error) {
	return s.policyRepo.List(ctx, f)
}

// GetPolicy 查询策略详情。
func (s *PolicyService) GetPolicy(ctx context.Context, policyID int64) (*model.CommandPolicy, error) {
	return s.policyRepo.Get(ctx, policyID)
}

// CreatePolicy 创建策略。
func (s *PolicyService) CreatePolicy(ctx context.Context, req CommandPolicyRequest, op OperatorContext) (*model.CommandPolicy, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	exists, err := s.policyRepo.ExistsByName(ctx, 0, req.PolicyName, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("策略名称已存在：%s", req.PolicyName)
	}
	p := &model.CommandPolicy{
		TenantID:   0,
		PolicyName: req.PolicyName,
		PolicyType: orDefault(req.PolicyType, policy.TypeBlacklist),
		MatchType:  orDefault(req.MatchType, policy.MatchRegex),
		Pattern:    req.Pattern,
		RiskLevel:  orDefault(req.RiskLevel, policy.RiskWarn),
		Action:     orDefault(req.Action, policy.ActionAudit),
		Scope:      req.Scope,
		Priority:   req.Priority,
		Enabled:    req.Enabled == nil || *req.Enabled,
		Remark:     req.Remark,
	}
	if p.Priority <= 0 {
		p.Priority = 100
	}
	p.TimeoutSeconds = req.TimeoutSeconds
	if p.TimeoutSeconds <= 0 {
		p.TimeoutSeconds = 30
	}
	if err := s.policyRepo.Create(ctx, p); err != nil {
		return nil, err
	}
	s.invalidateEngine()
	s.writeLog(ctx, op, "policy", "create", fmt.Sprint(p.PolicyID), p.PolicyName, "", toJSON(p))
	return p, nil
}

// UpdatePolicy 更新策略。
func (s *PolicyService) UpdatePolicy(ctx context.Context, policyID int64, req CommandPolicyRequest, op OperatorContext) (*model.CommandPolicy, error) {
	old, err := s.policyRepo.Get(ctx, policyID)
	if err != nil {
		return nil, err
	}
	// 部分更新：校验正则合法性、处置动作与匹配方式取值
	if err := req.validateRule(); err != nil {
		return nil, err
	}
	// 内置策略允许调整动作与启用状态，但不允许改匹配模式
	if old.IsBuiltin && req.Pattern != "" && req.Pattern != old.Pattern {
		return nil, fmt.Errorf("内置策略不允许修改匹配模式（可调整处置动作或停用）")
	}
	if req.Pattern != "" {
		old.Pattern = req.Pattern
	}
	before := toJSON(old)
	if req.PolicyName != "" {
		exists, err := s.policyRepo.ExistsByName(ctx, 0, req.PolicyName, policyID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, fmt.Errorf("策略名称已存在：%s", req.PolicyName)
		}
		old.PolicyName = req.PolicyName
	}
	if req.PolicyType != "" {
		old.PolicyType = req.PolicyType
	}
	if req.MatchType != "" {
		old.MatchType = req.MatchType
	}
	if req.RiskLevel != "" {
		old.RiskLevel = req.RiskLevel
	}
	if req.Action != "" {
		switch req.Action {
		case policy.ActionAllow, policy.ActionAudit, policy.ActionConfirm, policy.ActionDeny:
			old.Action = req.Action
		default:
			return nil, fmt.Errorf("处置动作非法：%s", req.Action)
		}
	}
	if req.Scope != nil {
		old.Scope = req.Scope
	}
	if req.Priority > 0 {
		old.Priority = req.Priority
	}
	if req.Enabled != nil {
		old.Enabled = *req.Enabled
	}
	if req.TimeoutSeconds > 0 {
		old.TimeoutSeconds = req.TimeoutSeconds
	}
	if req.Remark != "" {
		old.Remark = req.Remark
	}

	if err := s.policyRepo.Update(ctx, old); err != nil {
		return nil, err
	}
	s.invalidateEngine()
	s.writeLog(ctx, op, "policy", "update", fmt.Sprint(policyID), old.PolicyName, before, toJSON(old))
	return old, nil
}

// DeletePolicy 删除策略。
func (s *PolicyService) DeletePolicy(ctx context.Context, policyID int64, op OperatorContext) error {
	old, err := s.policyRepo.Get(ctx, policyID)
	if err != nil {
		return err
	}
	if err := s.policyRepo.Delete(ctx, policyID); err != nil {
		return err
	}
	s.invalidateEngine()
	s.writeLog(ctx, op, "policy", "delete", fmt.Sprint(policyID), old.PolicyName, toJSON(old), "")
	return nil
}

// TestCommandRequest 是命令测试请求。
type TestCommandRequest struct {
	Command    string `json:"command"`
	AssetID    int64  `json:"asset_id"`
	AssetLevel string `json:"asset_level"`
	UserID     int64  `json:"user_id"`
	Protocol   string `json:"protocol"`
}

// TestCommandResult 是命令测试结果。
type TestCommandResult struct {
	Command     string          `json:"command"`
	Normalized  string          `json:"normalized"`
	CommandName string          `json:"command_name"`
	Decision    *policy.Decision `json:"decision"`
}

// TestCommand 用当前策略测试某条命令的判定结果。
//
// 该接口对运维与安全人员非常实用：配置策略后可立即验证是否生效，
// 避免上线后才发现规则写错（匹配不到或误拦）。
func (s *PolicyService) TestCommand(ctx context.Context, req TestCommandRequest) (*TestCommandResult, error) {
	if req.Command == "" {
		return nil, fmt.Errorf("待测试命令不能为空")
	}
	engine, err := s.Engine(ctx)
	if err != nil {
		return nil, err
	}
	evalCtx := policy.EvalContext{
		AssetID:    req.AssetID,
		AssetLevel: req.AssetLevel,
		UserID:     req.UserID,
		Protocol:   req.Protocol,
	}
	return &TestCommandResult{
		Command:     req.Command,
		Normalized:  policy.NormalizeCommand(req.Command),
		CommandName: policy.ExtractCommandName(req.Command),
		Decision:    engine.Evaluate(evalCtx, req.Command),
	}, nil
}

// Engine 返回策略引擎（带缓存；策略变更时自动重建）。
func (s *PolicyService) Engine(ctx context.Context) (*policy.Engine, error) {
	s.mu.RLock()
	e := s.engine
	s.mu.RUnlock()
	if e != nil {
		return e, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.engine != nil {
		return s.engine, nil
	}
	rules, err := s.policyRepo.LoadEnabledRules(ctx, 0)
	if err != nil {
		return nil, err
	}
	// 白名单模式：默认动作改为 deny
	defaultAction := s.configRepo.GetString(ctx, 0, "policy.command.default_action", policy.ActionAllow)
	if s.configRepo.GetBool(ctx, 0, "policy.command.whitelist_mode", false) {
		defaultAction = policy.ActionDeny
	}
	engine, err := policy.NewEngine(rules, defaultAction)
	if err != nil {
		return nil, fmt.Errorf("策略引擎构建失败: %w", err)
	}
	s.engine = engine
	return engine, nil
}

// invalidateEngine 使引擎缓存失效（策略变更后调用）。
func (s *PolicyService) invalidateEngine() {
	s.mu.Lock()
	s.engine = nil
	s.mu.Unlock()
}

// Stats 返回策略统计。
func (s *PolicyService) Stats(ctx context.Context) (map[string]any, error) {
	byAction, err := s.policyRepo.CountByAction(ctx, 0)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, n := range byAction {
		total += n
	}
	engine, _ := s.Engine(ctx)
	ruleCount := 0
	defaultAction := policy.ActionAllow
	if engine != nil {
		ruleCount = engine.RuleCount()
		defaultAction = engine.DefaultAction()
	}
	return map[string]any{
		"enabled_total":  total,
		"by_action":      byAction,
		"engine_rules":   ruleCount,
		"default_action": defaultAction,
		"whitelist_mode": defaultAction == policy.ActionDeny,
	}, nil
}

// writeLog 写操作日志。
func (s *PolicyService) writeLog(ctx context.Context, op OperatorContext, module, action, targetID, targetName, before, after string) {
	if s.oplog == nil {
		return
	}
	l := &model.OperationLog{
		Module: module, Action: action, TargetType: "policy_command",
		TargetID: targetID, TargetName: targetName,
		BeforeValue: before, AfterValue: after,
		Result: "success", ClientIP: op.ClientIP, OperatorNameSnap: op.Username,
	}
	if op.UserID != 0 {
		uid := op.UserID
		l.OperatorID = &uid
	}
	_ = s.oplog.Write(ctx, l)
}

// orDefault 空串时返回默认值。
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
