package models_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"growth-lms/internal/models"
)

func TestParseMentionTokens(t *testing.T) {
	id1 := "11111111-1111-1111-1111-111111111111"
	id2 := "22222222-2222-2222-2222-222222222222"

	got := models.ParseMentionTokens("hey @[" + id1 + "] and @[" + id2 + "], also @[" + id1 + "] again")
	require.Equal(t, []string{id1, id2}, got, "dedupes, preserves first-seen order")

	require.Nil(t, models.ParseMentionTokens("no mentions here @alice"), "plain @name is not a token")
	require.Nil(t, models.ParseMentionTokens("@[not-a-uuid]"), "non-uuid token ignored")
}

func TestStripMentionTokens(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	require.Equal(t, "hi  there", models.StripMentionTokens("hi @["+id+"] there"))
	require.Equal(t, "no tokens", models.StripMentionTokens("no tokens"))
}
