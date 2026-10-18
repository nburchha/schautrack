package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"schautrack/internal/apierr"
	"schautrack/internal/middleware"
	"schautrack/internal/model"
	"schautrack/internal/service"
)

// --- Settings -------------------------------------------------------------

// v1SettingsPatch is the PATCH /me body. Only the settings that change how the
// rest of the API behaves are writable: the goal a client compares totals
// against, the timezone that decides what "today" means, the unit weights are
// stored in, and the UI language.
//
// Auth settings (password, 2FA, passkeys, email) are deliberately absent. Those
// are step-up gated in the app, and step-up has no meaning for a token — a
// bearer credential cannot "re-authenticate". Letting a token change them would
// turn one leaked token into account takeover.
type v1SettingsPatch struct {
	DailyGoal  Optional[int]    `json:"daily_goal"`
	Timezone   *string          `json:"timezone"`
	WeightUnit *string          `json:"weight_unit"`
	Language   Optional[string] `json:"language"`
	// Goals merges Basisziel macro goals (ADR 0007). Calories stay on
	// DailyGoal. Needs goals:write on top of settings:write.
	Goals *v1GoalsPatch `json:"goals"`
}

// v1GoalsPatch is the macro part of the Basisziel. Each key is Optional so an
// explicit null clears that goal while an absent key leaves it alone.
type v1GoalsPatch struct {
	Protein Optional[int] `json:"protein"`
	Carbs   Optional[int] `json:"carbs"`
	Fat     Optional[int] `json:"fat"`
	Fiber   Optional[int] `json:"fiber"`
	Sugar   Optional[int] `json:"sugar"`
}

// fields pairs each macro key with its patch value, in service.MacroKeys order.
func (g *v1GoalsPatch) fields() []struct {
	Key string
	Val Optional[int]
} {
	if g == nil {
		return nil
	}
	all := []Optional[int]{g.Protein, g.Carbs, g.Fat, g.Fiber, g.Sugar}
	var out []struct {
		Key string
		Val Optional[int]
	}
	for i, key := range service.MacroKeys {
		if all[i].Set {
			out = append(out, struct {
				Key string
				Val Optional[int]
			}{key, all[i]})
		}
	}
	return out
}

// UpdateMeV1 handles PATCH /api/v1/me.
func (h *V1Handler) UpdateMeV1(w http.ResponseWriter, r *http.Request) {
	var in v1SettingsPatch
	if prob := decodeV1(w, r, &in); prob != nil {
		apierr.Write(w, r, prob)
		return
	}

	var sets []string
	var args []any
	set := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}

	// Every change to users.macro_goals is composed into ONE expression: the
	// column can be assigned only once per UPDATE, and each step merges a key
	// into what the previous step produced. It is never replaced wholesale
	// (unlike the Settings form), so the *_mode keys and every macro goal not
	// named in the request survive.
	mgExpr := "COALESCE(macro_goals, '{}'::jsonb)"
	mgTouched := false
	putGoal := func(key string, v *int) {
		mgTouched = true
		if v == nil || *v == 0 { // null and 0 both mean "no goal": drop the key
			mgExpr = fmt.Sprintf("(%s - '%s')", mgExpr, key)
			return
		}
		args = append(args, *v)
		mgExpr = fmt.Sprintf("jsonb_set(%s, '{%s}', to_jsonb($%d::int))", mgExpr, key, len(args))
	}

	// The effective goal lives in macro_goals.calories (service.GetCalorieGoal
	// prefers it and ignores the legacy column once it exists), so that is the
	// key written here. The legacy column is nulled to match the startup
	// migration; only the calories key is touched.
	if in.DailyGoal.Set {
		if in.DailyGoal.Value == nil {
			set("daily_goal", nil)
			putGoal("calories", nil)
		} else if v := *in.DailyGoal.Value; v < 1 || v > MaxEntryCalories {
			apierr.Write(w, r, apierr.Unprocessable("The daily goal is out of range.",
				apierr.InvalidParam{
					Name:   "daily_goal",
					Reason: fmt.Sprintf("must be between 1 and %d", MaxEntryCalories),
				}))
			return
		} else {
			set("daily_goal", nil)
			putGoal("calories", &v)
		}
	}

	if macros := in.Goals.fields(); len(macros) > 0 {
		// Route scope is settings:write; changing the Basisziel macros is a
		// goals matter, so it needs its own scope. A token that may only change
		// the calorie goal or timezone must not be able to move macro targets.
		if token := middleware.GetAPIToken(r); token == nil || !service.ScopeSatisfies(token.Scopes, service.ScopeGoalsWrite) {
			apierr.Write(w, r, apierr.InsufficientScope(service.ScopeGoalsWrite))
			return
		}
		user := v1User(r)
		enabled := map[string]bool{}
		for _, k := range service.GetEnabledMacros(service.ParseMacroUser(user.MacrosEnabled, user.MacroGoals, user.DailyGoal, user.GoalThreshold)) {
			enabled[k] = true
		}
		for _, f := range macros {
			name := "goals." + f.Key
			if f.Val.Value != nil {
				if v := *f.Val.Value; v < 0 || v > service.MaxGoalMacro {
					apierr.Write(w, r, apierr.Unprocessable("The goal value is not usable.",
						apierr.InvalidParam{
							Name:   name,
							Reason: fmt.Sprintf("must be a whole number from 0 to %d (0 means no goal)", service.MaxGoalMacro),
						}))
					return
				}
				if !enabled[f.Key] {
					apierr.Write(w, r, apierr.Unprocessable("The goal value is not usable.",
						apierr.InvalidParam{Name: name, Reason: "this macro is not enabled for the account"}))
					return
				}
			}
			putGoal(f.Key, f.Val.Value)
		}
	}
	if mgTouched {
		sets = append(sets, "macro_goals = "+mgExpr)
	}

	if in.Timezone != nil {
		// Validated by loading it: an unknown zone would silently shift every
		// date boundary for this account.
		if _, err := time.LoadLocation(*in.Timezone); err != nil {
			apierr.Write(w, r, apierr.Unprocessable("That is not a known IANA time zone.",
				apierr.InvalidParam{Name: "timezone", Reason: "must be an IANA name such as Europe/Berlin"}))
			return
		}
		set("timezone", *in.Timezone)
		// Mirrors the app: an explicit choice must not be overwritten by the
		// browser's auto-detection on the next page load.
		set("timezone_manual", true)
	}

	if in.WeightUnit != nil {
		unit := strings.ToLower(strings.TrimSpace(*in.WeightUnit))
		if unit != "kg" && unit != "lb" {
			apierr.Write(w, r, apierr.Unprocessable("The weight unit is not recognised.",
				apierr.InvalidParam{Name: "weight_unit", Reason: `must be "kg" or "lb"`}))
			return
		}
		// Changing the unit does NOT convert stored readings — the app stores
		// them as entered. Switching units re-interprets existing numbers, so
		// this is a display setting, not a conversion.
		set("weight_unit", unit)
	}

	if in.Language.Set {
		if in.Language.Value == nil {
			set("language", nil)
		} else {
			lang := strings.ToLower(strings.TrimSpace(*in.Language.Value))
			if !supportedLanguages[lang] {
				apierr.Write(w, r, apierr.Unprocessable("That language is not supported.",
					apierr.InvalidParam{Name: "language", Reason: "must be one of " + languageList()}))
				return
			}
			set("language", lang)
		}
	}

	if len(sets) == 0 {
		apierr.Write(w, r, apierr.BadRequest("The request body contained no updatable fields."))
		return
	}

	user := v1User(r)
	args = append(args, user.ID)
	if _, err := h.Pool.Exec(r.Context(), fmt.Sprintf(
		"UPDATE users SET %s WHERE id = $%d", strings.Join(sets, ", "), len(args)), args...); err != nil {
		apierr.Write(w, r, dbFail("update settings", err))
		return
	}

	// Re-read so the response reflects committed state rather than what the
	// handler believes it wrote.
	fresh, err := middleware.GetUserByID(r.Context(), h.Pool, user.ID)
	if err != nil {
		apierr.Write(w, r, dbFail("reload user", err))
		return
	}
	writeV1(w, http.StatusOK, h.buildMe(r, fresh))
}

// languageList renders the supported set for an error message. Derived from
// the same supportedLanguages map the session settings handler validates
// against, so the two can never disagree about what is accepted.
func languageList() string {
	out := make([]string, 0, len(supportedLanguages))
	for l := range supportedLanguages {
		out = append(out, l)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// --- Barcode --------------------------------------------------------------

// BarcodeV1 handles GET /api/v1/barcode/{code}.
//
// It delegates to the same OpenFoodFacts lookup the app uses, so the API and
// the UI can never disagree about what a barcode resolves to — but it does NOT
// forward the response verbatim. The legacy handler speaks the legacy error
// shape, including a 200 carrying {"ok": false} for a misread check digit
// (deliberate there: a bad scan should prompt a rescan, not look like a
// failure). Passing that through would put a non-problem+json error on /api/v1
// and hand clients a 200 they must inspect the body to interpret.
//
// So the response is captured and translated: successes pass through, failures
// become problem details with a status that matches what actually happened.
func (h *V1Handler) BarcodeV1(w http.ResponseWriter, r *http.Request) {
	if h.Barcode == nil {
		apierr.Write(w, r, apierr.New(http.StatusNotFound, "feature-disabled",
			"Not found", "Barcode lookup is disabled on this server."))
		return
	}

	cap := &captureWriter{header: http.Header{}}
	h.Barcode(cap, r)

	var body map[string]any
	_ = json.Unmarshal(cap.body.Bytes(), &body)

	// The legacy handler signals failure with ok:false, whatever the status.
	failed := cap.status >= 400
	if ok, present := body["ok"].(bool); present && !ok {
		failed = true
	}

	if !failed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(cap.statusOr(http.StatusOK))
		w.Write(cap.body.Bytes())
		return
	}

	detail, _ := body["error"].(string)
	if detail == "" {
		detail = "The barcode could not be looked up."
	}

	switch {
	case body["code"] == "CHECK_DIGIT":
		// Well-formed digits that fail the GS1 checksum: the request is
		// syntactically fine but the value is not a real barcode.
		apierr.Write(w, r, apierr.Unprocessable(detail,
			apierr.InvalidParam{Name: "code", Reason: "check digit does not match"}))
	case cap.status == http.StatusNotFound || body["code"] == "NOT_FOUND":
		apierr.Write(w, r, apierr.NotFound(detail))
	case cap.status == http.StatusBadRequest:
		apierr.Write(w, r, apierr.BadRequest(detail))
	case cap.status >= 500:
		// Upstream trouble, not the caller's fault — say so with the status
		// the legacy handler chose (504 on timeout, 502 on a bad response).
		apierr.Write(w, r, apierr.New(cap.status, "upstream-unavailable",
			"Food database unavailable", detail))
	default:
		apierr.Write(w, r, apierr.NotFound(detail))
	}
}

// captureWriter buffers a handler's response instead of sending it, so the
// result can be inspected and rewritten. Unlike recorder (used for idempotency
// replay) it deliberately does NOT write through.
type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *captureWriter) Header() http.Header { return c.header }
func (c *captureWriter) WriteHeader(s int) {
	if c.status == 0 {
		c.status = s
	}
}
func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(b)
}
func (c *captureWriter) statusOr(fallback int) int {
	if c.status == 0 {
		return fallback
	}
	return c.status
}

// --- AI estimation --------------------------------------------------------

// EstimateV1 handles POST /api/v1/ai/estimate.
//
// Gated on its own ai:estimate scope, implied by nothing — every call spends
// the operator's money, so a token minted to log breakfast must not be able to
// run up an AI bill. The per-user daily cap the app already enforces applies
// unchanged, and the route carries its own per-account rate limit sized to the
// same ceiling the app's own /api/ai/estimate enforces (V1Handler.AILimiter):
// the API is not a cheaper path to the provider than the browser.
//
// That rate limit used not to exist, which made a token worth ~60x a logged-in
// session in estimates per minute, with the daily cap — unlimited by default
// outside the Helm chart — as the only backstop. See issue #292.
func (h *V1Handler) EstimateV1(w http.ResponseWriter, r *http.Request) {
	if h.AIEstimate == nil {
		apierr.Write(w, r, apierr.New(http.StatusNotFound, "feature-disabled",
			"Not found", "AI estimation is not configured on this server."))
		return
	}

	// Same reasoning as BarcodeV1: reuse the app's handler so billing, the
	// daily cap, and provider resolution have exactly one implementation, but
	// translate its legacy error shape rather than leaking it onto /api/v1.
	cap := &captureWriter{header: http.Header{}}
	h.AIEstimate(cap, r)

	var body map[string]any
	_ = json.Unmarshal(cap.body.Bytes(), &body)

	failed := cap.status >= 400
	if ok, present := body["ok"].(bool); present && !ok {
		failed = true
	}

	if !failed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(cap.statusOr(http.StatusOK))
		w.Write(cap.body.Bytes())
		return
	}

	detail, _ := body["error"].(string)
	if detail == "" {
		detail = "The estimate could not be produced."
	}

	// The daily cap is a quota, not a malformed request, so it maps to 429
	// with the same shape as a rate limit — a client backing off on 429
	// already handles it.
	if reached, _ := body["limitReached"].(bool); reached {
		p := apierr.TooManyRequests(detail)
		p.Type = "https://schautrack.com/problems/ai-daily-limit"
		p.Title = "Daily AI limit reached"
		apierr.Write(w, r, p)
		return
	}

	switch {
	case cap.status == http.StatusRequestEntityTooLarge:
		apierr.Write(w, r, apierr.New(cap.status, "body-too-large",
			"Image too large", detail))
	case cap.status == http.StatusBadRequest:
		apierr.Write(w, r, apierr.BadRequest(detail))
	case cap.status >= 500:
		apierr.Write(w, r, apierr.New(cap.status, "upstream-unavailable",
			"AI provider unavailable", detail))
	default:
		apierr.Write(w, r, apierr.BadRequest(detail))
	}
}

// buildMe assembles the /me payload in one place so GET and PATCH cannot drift
// apart.
func (h *V1Handler) buildMe(r *http.Request, user *model.User) v1Me {
	tz := user.GetTimezone()
	if tz == "" {
		tz = "UTC"
	}
	unit := user.WeightUnit
	if unit == "" {
		unit = "kg"
	}

	var out v1Me
	out.User.ID = user.ID
	out.User.Email = user.Email
	out.User.Timezone = tz
	out.User.WeightUnit = unit
	out.User.DailyGoal = service.GetCalorieGoal(
		service.ParseMacroUser(user.MacrosEnabled, user.MacroGoals, user.DailyGoal, user.GoalThreshold))
	out.User.Goals = toV1GoalValues(service.BaseGoal(user))
	out.User.Language = user.Language

	// The three stored booleans map straight across. Macros do not: the column
	// is a JSONB map of per-macro toggles, so both macro fields are derived
	// from it through the same service helpers the entry handlers use — a
	// second interpretation here would eventually disagree with the 422 that
	// UpdateEntryV1 returns, which is exactly the confusion #344 is about.
	mu := service.ParseMacroUser(user.MacrosEnabled, user.MacroGoals, user.DailyGoal, user.GoalThreshold)
	// Mirrors the nil checks BarcodeV1 and EstimateV1 make before answering
	// feature-disabled, so /me and the endpoints cannot disagree.
	out.Server.Features = v1ServerFeatures{
		Barcode:    h.Barcode != nil,
		AIEstimate: h.AIEstimate != nil,
	}

	out.Features = v1Features{
		BodyFat:          user.BodyFatEnabled,
		Todos:            user.TodosEnabled,
		Notes:            user.NotesEnabled,
		Macros:           len(service.GetEnabledMacros(mu)) > 0,
		AutoCalcCalories: service.IsAutoCalcCalories(mu),
	}

	if token := middleware.GetAPIToken(r); token != nil {
		out.Token.ID = token.ID
		out.Token.Name = token.Name
		out.Token.Prefix = token.Prefix
		out.Token.Scopes = token.Scopes
		out.Token.ExpiresAt = token.ExpiresAt
	}
	out.Server.Version = h.BuildVersion
	out.Server.Today = service.FormatDateInTz(time.Now(), tz)
	return out
}
