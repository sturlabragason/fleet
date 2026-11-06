package board

import "github.com/sturlabragason/fleet/internal/jsonx"

// Every type stored in board.json keeps the keys this build has no field
// for. Several processes of different vintages write the board — a resident
// watcher, the overview, every MCP server — and before ADR-58 the oldest of
// them silently erased whatever a newer one had written, which is how a
// leader lost its `leader` flag seconds after being spawned. ADR-58 covered
// Agent; ADR-62 covers the rest. Each alias type below is its struct without
// methods, so jsonx can use the ordinary encoding without recursing.

type (
	agentJSON    Agent
	boardJSON    Board
	cardJSON     Card
	checkJSON    Check
	worktreeJSON Worktree
	exitJSON     Exit
	gitBaseJSON  GitBase
	reportJSON   Report
)

func (a *Agent) UnmarshalJSON(b []byte) error {
	var v agentJSON
	e, err := jsonx.Unmarshal(b, &v)
	*a, a.extra = Agent(v), e
	return err
}

func (a Agent) MarshalJSON() ([]byte, error) { return jsonx.Marshal(agentJSON(a), a.extra) }

func (x *Board) UnmarshalJSON(b []byte) error {
	var v boardJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Board(v), e
	return err
}

func (x Board) MarshalJSON() ([]byte, error) { return jsonx.Marshal(boardJSON(x), x.extra) }

func (x *Card) UnmarshalJSON(b []byte) error {
	var v cardJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Card(v), e
	return err
}

func (x Card) MarshalJSON() ([]byte, error) { return jsonx.Marshal(cardJSON(x), x.extra) }

func (x *Check) UnmarshalJSON(b []byte) error {
	var v checkJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Check(v), e
	return err
}

func (x Check) MarshalJSON() ([]byte, error) { return jsonx.Marshal(checkJSON(x), x.extra) }

func (x *Worktree) UnmarshalJSON(b []byte) error {
	var v worktreeJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Worktree(v), e
	return err
}

func (x Worktree) MarshalJSON() ([]byte, error) { return jsonx.Marshal(worktreeJSON(x), x.extra) }

func (x *Exit) UnmarshalJSON(b []byte) error {
	var v exitJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Exit(v), e
	return err
}

func (x Exit) MarshalJSON() ([]byte, error) { return jsonx.Marshal(exitJSON(x), x.extra) }

func (x *GitBase) UnmarshalJSON(b []byte) error {
	var v gitBaseJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = GitBase(v), e
	return err
}

func (x GitBase) MarshalJSON() ([]byte, error) { return jsonx.Marshal(gitBaseJSON(x), x.extra) }

func (x *Report) UnmarshalJSON(b []byte) error {
	var v reportJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Report(v), e
	return err
}

func (x Report) MarshalJSON() ([]byte, error) { return jsonx.Marshal(reportJSON(x), x.extra) }
