package ai

import (
	"context"
	"fmt"
	"strings"
)

// Service turns the four product operations into versioned prompts, calls
// the configured Provider, and returns the text plus everything the caller
// needs to log a row in ai_generations (provider, model, prompt version,
// token counts, cost). It holds no database handle by design.
type Service struct {
	provider Provider
}

// NewService wraps a Provider.
func NewService(p Provider) *Service { return &Service{provider: p} }

// ProviderName / Model expose the backing provider's identity for callers
// that want to surface it (e.g. the usage dashboard).
func (s *Service) ProviderName() string { return s.provider.Name() }
func (s *Service) Model() string        { return s.provider.Model() }

// Output is the result of one generation together with its accounting.
type Output struct {
	Text          string
	Provider      string
	Model         string
	PromptVersion string
	InputTokens   int
	OutputTokens  int
	CostMicros    int64
}

func (s *Service) run(ctx context.Context, promptVersion string, req Request) (Output, error) {
	res, err := s.provider.Generate(ctx, req)
	if err != nil {
		return Output{}, err
	}
	model := s.provider.Model()
	return Output{
		Text:          res.Text,
		Provider:      s.provider.Name(),
		Model:         model,
		PromptVersion: promptVersion,
		InputTokens:   res.InputTokens,
		OutputTokens:  res.OutputTokens,
		CostMicros:    CostMicros(model, res.InputTokens, res.OutputTokens),
	}, nil
}

// ---------------------------------------------------------------------------
// Prompt versions. Bump the suffix whenever a template changes so the
// ai_generations ledger records exactly which prompt produced a row.
// ---------------------------------------------------------------------------

const (
	PromptOutlineV1 = "outline/v1"
	PromptLessonV1  = "lesson/v1"
	PromptQuizV1    = "quiz/v1"
	PromptTutorV1   = "tutor/v1"
)

// OutlineInput drives course-outline generation.
type OutlineInput struct {
	Topic    string
	Audience string
	Modules  int
}

// Outline drafts a structured course outline (markdown) for a topic.
func (s *Service) Outline(ctx context.Context, in OutlineInput) (Output, error) {
	modules := in.Modules
	if modules <= 0 || modules > 20 {
		modules = 6
	}
	audience := strings.TrimSpace(in.Audience)
	if audience == "" {
		audience = "a general adult audience new to the topic"
	}
	system := "You are an expert instructional designer. You produce clear, " +
		"pedagogically-sound course outlines. Respond with the outline only, in " +
		"Markdown, with no preamble or closing commentary."
	user := fmt.Sprintf(
		"Create a course outline on \"%s\" for %s.\n\n"+
			"Produce exactly %d modules. For each module give a short title and 3-5 "+
			"lesson titles as a bulleted list. Order modules from foundational to advanced.",
		strings.TrimSpace(in.Topic), audience, modules)
	return s.run(ctx, PromptOutlineV1, Request{
		System:    system,
		Messages:  []Message{{Role: RoleUser, Content: user}},
		MaxTokens: 2048,
	})
}

// LessonInput drives lesson-draft generation.
type LessonInput struct {
	CourseTitle string
	LessonTitle string
	Objectives  string
}

// Lesson drafts the body of a single lesson (Markdown) from its title and
// learning objectives.
func (s *Service) Lesson(ctx context.Context, in LessonInput) (Output, error) {
	system := "You are an expert course author writing lesson content for an " +
		"online learning platform. Write in clear, engaging Markdown suitable for " +
		"adult learners. Respond with the lesson body only — no preamble, no title " +
		"heading (the platform renders the title separately)."
	var b strings.Builder
	fmt.Fprintf(&b, "Write a lesson titled \"%s\".\n", strings.TrimSpace(in.LessonTitle))
	if t := strings.TrimSpace(in.CourseTitle); t != "" {
		fmt.Fprintf(&b, "It belongs to the course \"%s\".\n", t)
	}
	if o := strings.TrimSpace(in.Objectives); o != "" {
		fmt.Fprintf(&b, "Cover these learning objectives:\n%s\n", o)
	}
	b.WriteString("\nInclude a short introduction, the core explanation with examples, " +
		"and a brief summary. Aim for 400-800 words.")
	return s.run(ctx, PromptLessonV1, Request{
		System:    system,
		Messages:  []Message{{Role: RoleUser, Content: b.String()}},
		MaxTokens: 3072,
	})
}

// QuizInput drives quiz generation.
type QuizInput struct {
	Topic         string
	SourceContent string
	NumQuestions  int
}

// Quiz generates a multiple-choice quiz. The model is asked to return a
// JSON array; the handler parses it into quiz blocks (and falls back to raw
// text if parsing fails), so the contract is documented in the prompt.
func (s *Service) Quiz(ctx context.Context, in QuizInput) (Output, error) {
	n := in.NumQuestions
	if n <= 0 || n > 20 {
		n = 5
	}
	system := "You are an assessment designer. You write fair, unambiguous " +
		"multiple-choice questions with exactly one correct answer each. " +
		"Respond with ONLY a JSON array and no other text. Each element must be an " +
		"object with keys: \"question\" (string), \"options\" (array of 4 strings), " +
		"and \"correct_index\" (integer 0-3)."
	var b strings.Builder
	fmt.Fprintf(&b, "Write %d multiple-choice questions", n)
	if t := strings.TrimSpace(in.Topic); t != "" {
		fmt.Fprintf(&b, " on the topic \"%s\"", t)
	}
	b.WriteString(".")
	if src := strings.TrimSpace(in.SourceContent); src != "" {
		fmt.Fprintf(&b, "\n\nBase the questions on this lesson content:\n%s", src)
	}
	return s.run(ctx, PromptQuizV1, Request{
		System:    system,
		Messages:  []Message{{Role: RoleUser, Content: b.String()}},
		MaxTokens: 3072,
	})
}

// TutorInput drives a course-scoped tutor reply.
type TutorInput struct {
	CourseTitle       string
	CourseDescription string
	History           []Message
	Question          string
}

// Tutor answers a learner's question, scoped to one course. The system
// prompt pins the tutor to the course context and instructs it to decline
// off-topic questions, so the tutor stays a study aid rather than a general
// chatbot.
func (s *Service) Tutor(ctx context.Context, in TutorInput) (Output, error) {
	var sys strings.Builder
	fmt.Fprintf(&sys, "You are a friendly, patient tutor for the course \"%s\".",
		strings.TrimSpace(in.CourseTitle))
	if d := strings.TrimSpace(in.CourseDescription); d != "" {
		fmt.Fprintf(&sys, " Course description: %s.", d)
	}
	sys.WriteString(" Help the learner understand the material with clear explanations " +
		"and examples. If a question is unrelated to this course, gently steer the " +
		"learner back to the course topics instead of answering. Do not invent facts; " +
		"if you are unsure, say so.")

	msgs := make([]Message, 0, len(in.History)+1)
	msgs = append(msgs, in.History...)
	msgs = append(msgs, Message{Role: RoleUser, Content: strings.TrimSpace(in.Question)})

	return s.run(ctx, PromptTutorV1, Request{
		System:    sys.String(),
		Messages:  msgs,
		MaxTokens: 1536,
	})
}
