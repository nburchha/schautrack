package handler

import (
	"errors"
	"net/http"
	"time"

	"schautrack/internal/apierr"
	"schautrack/internal/service"
)

// v1GoalValues is the six-number goal, each null when no goal is set.
type v1GoalValues struct {
	Calories *int `json:"calories"`
	Protein  *int `json:"protein"`
	Carbs    *int `json:"carbs"`
	Fat      *int `json:"fat"`
	Fiber    *int `json:"fiber"`
	Sugar    *int `json:"sugar"`
}

func toV1GoalValues(v service.GoalValues) v1GoalValues {
	return v1GoalValues{Calories: v.Calories, Protein: v.Protein, Carbs: v.Carbs, Fat: v.Fat, Fiber: v.Fiber, Sugar: v.Sugar}
}

// v1Goal is the Tagesziel of one date: the day's adjustment when there is one,
// otherwise the Basisziel. The values are flattened (not nested) so a client
// reads the goal the same way whether or not the day is adjusted; Base is only
// there to show what an adjusted day differs from.
type v1Goal struct {
	Date     string  `json:"date"`
	Adjusted bool    `json:"adjusted"`
	Reason   *string `json:"reason"`
	v1GoalValues
	Base      *v1GoalValues `json:"base"`
	UpdatedAt *time.Time    `json:"updated_at"`
}

func toV1Goal(g *service.DayGoal) v1Goal {
	out := v1Goal{
		Date: g.Date, Adjusted: g.Adjusted, Reason: g.Reason,
		v1GoalValues: toV1GoalValues(g.GoalValues), UpdatedAt: g.UpdatedAt,
	}
	if g.Base != nil {
		b := toV1GoalValues(*g.Base)
		out.Base = &b
	}
	return out
}

// v1GoalInput is the thin body of PUT /goals/{date}: send what differs, the
// rest comes from the Basisziel. Plain pointers are right here (unlike PATCH
// bodies): absent and null both mean "not sent", and there is no way to clear
// a single field of a snapshot.
type v1GoalInput struct {
	Calories *int    `json:"calories"`
	Protein  *int    `json:"protein"`
	Carbs    *int    `json:"carbs"`
	Fat      *int    `json:"fat"`
	Fiber    *int    `json:"fiber"`
	Sugar    *int    `json:"sugar"`
	Reason   *string `json:"reason"`
}

func (in v1GoalInput) toService() service.GoalInput {
	return service.GoalInput{
		GoalValues: service.GoalValues{
			Calories: in.Calories, Protein: in.Protein, Carbs: in.Carbs,
			Fat: in.Fat, Fiber: in.Fiber, Sugar: in.Sugar,
		},
		Reason: in.Reason,
	}
}

// GetGoalV1 handles GET /api/v1/goals/{date}.
func (h *V1Handler) GetGoalV1(w http.ResponseWriter, r *http.Request) {
	date, prob := pathDate(r)
	if prob != nil {
		apierr.Write(w, r, prob)
		return
	}
	g, err := service.ResolveDayGoal(r.Context(), h.Pool, v1User(r), date)
	if err != nil {
		apierr.Write(w, r, dbFail("resolve goal", err))
		return
	}
	writeV1(w, http.StatusOK, toV1Goal(g))
}

// DeleteGoalV1 handles DELETE /api/v1/goals/{date} — resets the day to the
// Basisziel. Unlike weight it answers 204 when there was nothing to delete: the
// caller's intent ("this day has no adjustment") holds either way.
func (h *V1Handler) DeleteGoalV1(w http.ResponseWriter, r *http.Request) {
	date, prob := pathDate(r)
	if prob != nil {
		apierr.Write(w, r, prob)
		return
	}
	user := v1User(r)
	if err := service.DeleteDayGoal(r.Context(), h.Pool, user.ID, date); err != nil {
		apierr.Write(w, r, dbFail("delete goal", err))
		return
	}
	h.broadcastEntries(user.ID)
	noContent(w)
}

// PutGoalV1 handles PUT /api/v1/goals/{date} — replaces the day's adjustment.
//
// Naturally idempotent (the date identifies the resource and a repeat writes
// the same snapshot), so it is not wrapped in withIdempotency. 201 on the
// date's first adjustment, 200 on replacement.
func (h *V1Handler) PutGoalV1(w http.ResponseWriter, r *http.Request) {
	date, prob := pathDate(r)
	if prob != nil {
		apierr.Write(w, r, prob)
		return
	}
	var in v1GoalInput
	if prob := decodeV1(w, r, &in); prob != nil {
		apierr.Write(w, r, prob)
		return
	}

	user := v1User(r)
	g, created, err := service.SetDayGoal(r.Context(), h.Pool, user, date, in.toService())
	var verr *service.GoalValidationError
	if errors.As(err, &verr) {
		if verr.Field == "" {
			apierr.Write(w, r, apierr.Unprocessable(verr.Reason))
		} else {
			apierr.Write(w, r, apierr.Unprocessable("The goal value is not usable.",
				apierr.InvalidParam{Name: verr.Field, Reason: verr.Reason}))
		}
		return
	}
	if err != nil {
		apierr.Write(w, r, dbFail("set goal", err))
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/goals/"+date)
	}
	h.broadcastEntries(user.ID)
	writeV1(w, status, toV1Goal(g))
}
