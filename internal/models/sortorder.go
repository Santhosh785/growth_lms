package models

// sort_order columns are NUMERIC(20,10) in Postgres for exact storage, but
// Go-side we only ever need them for ordering and midpoint arithmetic —
// float64's ~15-17 significant digits is far more precision than any
// realistic sibling count will ever exhaust in practice, so we represent
// them as float64 in Go rather than pulling in a decimal library. The
// renormalizeThreshold below is deliberately generous: it triggers a
// whole-number respacing long before float64 precision would actually
// become a problem.

// renormalizeThreshold is the minimum gap between two adjacent sort_order
// values below which inserting a fractional midpoint is considered to be
// exhausting the available precision, and all siblings should be
// renormalized to whole-number spacing (1.0, 2.0, 3.0, ...) instead.
const renormalizeThreshold = 1e-6

// NextSortOrder returns the sort_order value to use when appending a new
// sibling to the end of an ordered list (existing, sorted ascending).
func NextSortOrder(existing []float64) float64 {
	if len(existing) == 0 {
		return 1.0
	}
	return existing[len(existing)-1] + 1.0
}

// MidpointSortOrder returns the sort_order value to use when inserting a
// new sibling strictly between before and after, and whether the caller
// must renormalize all siblings first because the gap between before and
// after has been exhausted (in which case the returned value should be
// ignored — call Renormalize and retry against the renormalized values).
func MidpointSortOrder(before, after float64) (value float64, needsRenormalize bool) {
	if after-before <= renormalizeThreshold {
		return 0, true
	}
	return before + (after-before)/2, false
}

// NeedsRenormalize reports whether any adjacent gap in a sorted-ascending
// list of sibling sort_order values has been exhausted to the point that a
// future midpoint insert (or a reorder pushing two values this close
// together) would no longer have room — the caller should renormalize the
// whole set to whole-number spacing in the same transaction.
func NeedsRenormalize(sortedAscending []float64) bool {
	for i := 1; i < len(sortedAscending); i++ {
		if sortedAscending[i]-sortedAscending[i-1] <= renormalizeThreshold {
			return true
		}
	}
	return false
}

// Renormalize returns whole-number sort_order values (1.0, 2.0, 3.0, ...)
// for siblings in the given order, for the caller to persist in one
// transaction alongside whatever insert/reorder triggered the
// renormalization.
func Renormalize(count int) []float64 {
	out := make([]float64, count)
	for i := range out {
		out[i] = float64(i + 1)
	}
	return out
}
