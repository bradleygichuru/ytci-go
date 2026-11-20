package expo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitEqualRemainder(t *testing.T) {
	shares, err := splitEqual(10000, []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, int64(3334), shares["a"])
	assert.Equal(t, int64(3333), shares["b"])
	assert.Equal(t, int64(3333), shares["c"])
	var sum int64
	for _, v := range shares {
		sum += v
	}
	assert.Equal(t, int64(10000), sum)
}

func TestSplitCustomValidation(t *testing.T) {
	_, _, err := splitCustom(10000, []shareInput{
		{ParticipantID: "a", AmountKes: 4000},
		{ParticipantID: "b", AmountKes: 5000},
	})
	assert.ErrorIs(t, err, errSplitMismatch)

	_, _, err = splitCustom(10000, []shareInput{
		{ParticipantID: "a", AmountKes: 4000},
		{ParticipantID: "a", AmountKes: 6000},
	})
	assert.ErrorIs(t, err, errDuplicateShare)
}

func TestSplitPercentRemainder(t *testing.T) {
	shares, percents, err := splitPercent(10000, []shareInput{
		{ParticipantID: "a", Percent: 33.33},
		{ParticipantID: "b", Percent: 33.33},
		{ParticipantID: "c", Percent: 33.34},
	})
	require.NoError(t, err)
	assert.Equal(t, 33.33, percents["a"])
	assert.Equal(t, int64(10000), shares["a"]+shares["b"]+shares["c"])
	assert.Equal(t, int64(3334), shares["c"])
}

func TestSplitPercentMustTotalOneHundred(t *testing.T) {
	_, _, err := splitPercent(10000, []shareInput{
		{ParticipantID: "a", Percent: 50},
		{ParticipantID: "b", Percent: 40},
	})
	assert.ErrorIs(t, err, errSplitMismatch)
}

func TestRoundKES(t *testing.T) {
	assert.Equal(t, int64(121), roundKES(100.50, 1.2))
	assert.Equal(t, int64(1), roundKES(0.6, 1))
}

func TestSuggestTransfers(t *testing.T) {
	transfers := suggestTransfers(map[string]int64{
		"a": 6666,
		"b": -3333,
		"c": -3333,
	}, []string{"a", "b", "c"})
	require.Len(t, transfers, 2)
	assert.Equal(t, transfer{FromID: "b", ToID: "a", Amount: 3333}, transfers[0])
	assert.Equal(t, transfer{FromID: "c", ToID: "a", Amount: 3333}, transfers[1])
}

func TestBudgetCategoryAndCurrencyValidation(t *testing.T) {
	assert.True(t, isValidBudgetCategory("Park & entry fees"))
	assert.False(t, isValidBudgetCategory("Snacks"))
	assert.True(t, isValidCurrency("KES"))
	assert.False(t, isValidCurrency("CHF"))
}

func TestSplitMathTable(t *testing.T) {
	equalCases := []struct {
		name     string
		amount   int64
		ids      []string
		expected map[string]int64
	}{
		{"even", 10000, []string{"a", "b"}, map[string]int64{"a": 5000, "b": 5000}},
		{"remainder", 10000, []string{"a", "b", "c"}, map[string]int64{"a": 3334, "b": 3333, "c": 3333}},
		{"single", 450, []string{"a"}, map[string]int64{"a": 450}},
	}
	for _, tc := range equalCases {
		t.Run("equal/"+tc.name, func(t *testing.T) {
			got, err := splitEqual(tc.amount, tc.ids)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, got)
		})
	}

	percentCases := []struct {
		name     string
		amount   int64
		percents []float64
		expected map[string]int64
	}{
		{"thirds", 10000, []float64{33.33, 33.33, 33.34}, map[string]int64{"a": 3333, "b": 3333, "c": 3334}},
		{"halves", 101, []float64{50, 50}, map[string]int64{"a": 51, "b": 50}},
	}
	for _, tc := range percentCases {
		t.Run("percent/"+tc.name, func(t *testing.T) {
			shares := make([]shareInput, 0, len(tc.percents))
			for i, p := range tc.percents {
				shares = append(shares, shareInput{ParticipantID: string(rune('a' + i)), Percent: p})
			}
			got, _, err := splitPercent(tc.amount, shares)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, got)
		})
	}
}
