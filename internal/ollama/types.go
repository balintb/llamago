package ollama

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Message is a single turn in a chat conversation.
//
// A tool result is a message with Role "tool" and ToolName set to the tool that produced it; a request to call one arrives as ToolCalls on an assistant message, usually with empty Content.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	Images    []string   `json:"images,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

// ToolCall is a model asking for a tool to be run. Ollama sends the arguments as a JSON object rather than as an encoded string.
type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Index     int            `json:"index,omitempty"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// Tool is one entry in the list a request offers the model. Type is always "function"; Ollama defines no other kind.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a tool to the model. Parameters is JSON Schema, passed through as written so a tool can describe whatever shape it takes.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Options are the per-request inference knobs Ollama accepts.
type Options struct {
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	TopK          *int     `json:"top_k,omitempty"`
	RepeatPenalty *float64 `json:"repeat_penalty,omitempty"`
	NumCtx        *int     `json:"num_ctx,omitempty"`
	NumPredict    *int     `json:"num_predict,omitempty"`
	Seed          *int     `json:"seed,omitempty"`
	Stop          []string `json:"stop,omitempty"`
}

// ChatRequest is the body of POST /api/chat.
type ChatRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`
	Think     *bool     `json:"think,omitempty"`
	Tools     []Tool    `json:"tools,omitempty"`
	KeepAlive string    `json:"keep_alive,omitempty"`
	Options   *Options  `json:"options,omitempty"`
}

// ChatResponse is one streamed chunk from /api/chat. Timing fields are only populated on the final chunk (Done == true).
type ChatResponse struct {
	Model      string    `json:"model"`
	CreatedAt  time.Time `json:"created_at"`
	Message    Message   `json:"message"`
	Done       bool      `json:"done"`
	DoneReason string    `json:"done_reason,omitempty"`

	TotalDuration      int64 `json:"total_duration,omitempty"`
	LoadDuration       int64 `json:"load_duration,omitempty"`
	PromptEvalCount    int   `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64 `json:"prompt_eval_duration,omitempty"`
	EvalCount          int   `json:"eval_count,omitempty"`
	EvalDuration       int64 `json:"eval_duration,omitempty"`
}

// TokensPerSecond derives generation throughput from the final chunk.
func (r ChatResponse) TokensPerSecond() float64 {
	if r.EvalDuration <= 0 || r.EvalCount <= 0 {
		return 0
	}
	return float64(r.EvalCount) / (float64(r.EvalDuration) / 1e9)
}

// Details describes a model's architecture and quantization.
type Details struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

// Model is an entry from GET /api/tags.
type Model struct {
	Name       string    `json:"name"`
	Model      string    `json:"model"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
	Digest     string    `json:"digest"`
	Details    Details   `json:"details"`
}

// ShortName strips the ":latest" suffix that adds noise to every list.
func (m Model) ShortName() string {
	return strings.TrimSuffix(m.Name, ":latest")
}

// Tag returns just the tag portion of the model name.
func (m Model) Tag() string {
	if i := strings.LastIndex(m.Name, ":"); i >= 0 {
		return m.Name[i+1:]
	}
	return "latest"
}

// RunningModel is an entry from GET /api/ps: a model currently held in memory.
type RunningModel struct {
	Name          string    `json:"name"`
	Model         string    `json:"model"`
	Size          int64     `json:"size"`
	SizeVRAM      int64     `json:"size_vram"`
	Digest        string    `json:"digest"`
	Details       Details   `json:"details"`
	ExpiresAt     time.Time `json:"expires_at"`
	ContextLength int       `json:"context_length,omitempty"`
}

// GPUPercent is the share of the model resident on GPU rather than in system RAM.
func (r RunningModel) GPUPercent() float64 {
	if r.Size <= 0 {
		return 0
	}
	return float64(r.SizeVRAM) / float64(r.Size)
}

// Processor summarizes the CPU/GPU split the way `ollama ps` does.
func (r RunningModel) Processor() string {
	switch p := r.GPUPercent(); {
	case r.SizeVRAM == 0:
		return "100% CPU"
	case p > 0.99:
		return "100% GPU"
	default:
		return fmt.Sprintf("%d%%/%d%% CPU/GPU", int((1-p)*100), int(p*100))
	}
}

// ShowResponse is the body of POST /api/show.
type ShowResponse struct {
	License      string         `json:"license"`
	Modelfile    string         `json:"modelfile"`
	Parameters   string         `json:"parameters"`
	Template     string         `json:"template"`
	System       string         `json:"system"`
	Details      Details        `json:"details"`
	ModelInfo    map[string]any `json:"model_info"`
	Capabilities []string       `json:"capabilities"`
}

// ContextLength digs the architecture-specific context length out of model_info.
func (s ShowResponse) ContextLength() int {
	for k, v := range s.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			if f, ok := v.(float64); ok {
				return int(f)
			}
		}
	}
	return 0
}

// ParameterCount reads the total parameter count reported by the server.
func (s ShowResponse) ParameterCount() int64 {
	if v, ok := s.ModelInfo["general.parameter_count"].(float64); ok {
		return int64(v)
	}
	return 0
}

// CanThink reports whether the model exposes a reasoning/thinking channel.
func (s ShowResponse) CanThink() bool { return s.HasCapability("thinking") }

// CanVision reports whether the model accepts images as input.
func (s ShowResponse) CanVision() bool { return s.HasCapability("vision") }

// CanImage reports whether the model generates images rather than text. These need a diffusion runtime, which is a different thing from a chat model that can look at pictures.
func (s ShowResponse) CanImage() bool { return s.HasCapability("image") }

// CanChat reports whether the model can hold a conversation at all.
func (s ShowResponse) CanChat() bool {
	return s.HasCapability("completion") || len(s.Capabilities) == 0
}

// HasCapability reports whether the server advertised the named capability.
func (s ShowResponse) HasCapability(name string) bool {
	return slices.Contains(s.Capabilities, name)
}

// PullProgress is one streamed chunk from POST /api/pull.
type PullProgress struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// Percent is the fraction of this layer that has been downloaded.
func (p PullProgress) Percent() float64 {
	if p.Total <= 0 {
		return 0
	}
	return float64(p.Completed) / float64(p.Total)
}

// HumanBytes formats a byte count with binary units, e.g. "4.7 GB".
func HumanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTP"[exp])
}

// HumanCount formats a parameter count compactly, e.g. "8.0B".
func HumanCount(n int64) string {
	switch {
	case n >= 1e12:
		return fmt.Sprintf("%.1fT", float64(n)/1e12)
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.0fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// HumanSince formats a timestamp as a coarse relative age, e.g. "3 days ago".
func HumanSince(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		return plural(int(d.Hours()/(24*30)), "month")
	default:
		return plural(int(d.Hours()/(24*365)), "year")
	}
}

// HumanUntil formats a deadline as a short countdown, e.g. "4m30s".
func HumanUntil(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return "expired"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit + " ago"
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}
