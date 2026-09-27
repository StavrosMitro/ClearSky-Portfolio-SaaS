// Command seed fills a running ClearSky with fake but complete data through
// the public API, exactly as people would: the secretariat registers the
// institution, buys credits, imports the student registry and invites
// instructors; students register and activate their accounts from the
// emailed links (read from Mailpit); instructors post initial grades,
// students ask for reviews, instructors answer and post final grades.
//
// It is deterministic (-seed) and can be re-run: existing accounts sign in,
// published gradings and filed requests are left as they are.
//
//	set -a; . ./.env; set +a
//	go run ./tools/cmd/seed                       # from the repository root
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"strings"
	"time"

	"clearsky/tools/api"
	"clearsky/tools/workbook"
)

type course struct {
	workbook.Course
	instructor int
	final      bool // finalised after the reviews
}

var courses = []course{
	{workbook.Course{Code: "3101", Title: "ΦΥΣΙΚΗ", Period: "2024-2025 ΧΕΙΜ 2024", Weights: []float64{0.4, 0.6, 1.0, 0.8}}, 0, true},
	{workbook.Course{Code: "3001", Title: "ΜΑΘΗΜΑΤΙΚΗ ΑΝΑΛΥΣΗ", Period: "2024-2025 ΧΕΙΜ 2024", Weights: []float64{1.0, 1.0, 1.0}}, 2, true},
	{workbook.Course{Code: "3110", Title: "ΑΛΓΟΡΙΘΜΟΙ", Period: "2024-2025 ΧΕΙΜ 2024", Weights: []float64{0.6, 0.8, 1.0, 1.2, 0.4}}, 2, false},
	{workbook.Course{Code: "3302", Title: "ΒΑΣΕΙΣ ΔΕΔΟΜΕΝΩΝ", Period: "2024-2025 ΕΑΡ 2025", Weights: []float64{0.4, 0.6, 0.8, 1.0, 1.0, 1.2}}, 1, true},
	{workbook.Course{Code: "3205", Title: "ΤΕΧΝΟΛΟΓΙΑ ΛΟΓΙΣΜΙΚΟΥ", Period: "2024-2025 ΕΑΡ 2025", Weights: []float64{0.2, 0.4, 0.6, 0.8, 1.0, 1.2, 0.2, 0.4, 0.6, 0.8}}, 1, false},
	{workbook.Course{Code: "3401", Title: "ΔΙΚΤΥΑ ΥΠΟΛΟΓΙΣΤΩΝ", Period: "2024-2025 ΕΑΡ 2025", Weights: []float64{0.8, 0.8, 1.2, 1.2}}, 0, false},
}

var (
	firstNames = []string{"Γιώργος", "Μαρία", "Νίκος", "Ελένη", "Κώστας", "Άννα", "Δημήτρης", "Σοφία", "Γιάννης", "Κατερίνα", "Παναγιώτης", "Χριστίνα"}
	lastNames  = []string{"Παπαδόπουλος", "Ιωάννου", "Γεωργίου", "Νικολάου", "Δημητρίου", "Κωνσταντίνου", "Αλεξίου", "Βασιλείου"}
	messages   = []string{
		"Θα ήθελα να δω ξανά το θέμα 2· πιστεύω ότι η λύση μου ήταν σωστή.",
		"Παρακαλώ ελέγξτε τη βαθμολογία του τελευταίου θέματος.",
		"Νομίζω ότι υπάρχει λάθος στην άθροιση των μονάδων.",
		"Θα ήθελα να συζητήσουμε τη βαθμολογία μου στις ώρες γραφείου.",
	}
	actions = []string{"total_accept", "partial_accept", "reject"}
	replies = map[string]string{
		"total_accept":   "Έχετε δίκιο· ο βαθμός διορθώνεται στην τελική βαθμολογία.",
		"partial_accept": "Δίνονται επιπλέον μονάδες σε μέρος του θέματος.",
		"reject":         "Η βαθμολόγηση είναι σύμφωνη με τις οδηγίες διόρθωσης.",
	}
)

type student struct {
	api.RosterEntry
	session *api.Session
}

func main() {
	var (
		baseURL     = flag.String("url", env("SEED_URL", "https://localhost"), "public address (the proxy)")
		mailURL     = flag.String("mailpit", env("SEED_MAILPIT", "http://127.0.0.1:8025"), "Mailpit address")
		insecure    = flag.Bool("insecure", true, "accept Caddy's local certificate authority")
		admin       = flag.String("admin", os.Getenv("BOOTSTRAP_ADMIN_USERNAME"), "secretariat username")
		adminPass   = flag.String("admin-password", os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"), "secretariat password")
		institution = flag.String("institution", env("BOOTSTRAP_INSTITUTION", "ClearSky"), "institution name")
		domain      = flag.String("domain", "uni.example", "email domain of the fake accounts")
		count       = flag.Int("students", 30, "number of students")
		password    = flag.String("password", os.Getenv("SEED_PASSWORD"), "password of every seeded account (random when empty)")
		seed        = flag.Uint64("seed", 2026, "random seed")
	)
	flag.Parse()
	if *admin == "" || *adminPass == "" {
		log.Fatal("the secretariat account is required: -admin/-admin-password or BOOTSTRAP_ADMIN_USERNAME/PASSWORD")
	}
	if *password == "" {
		raw := make([]byte, 8)
		_, _ = rand.Read(raw)
		*password = "Seed-" + hex.EncodeToString(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	p := api.Platform{Config: api.Config{BaseURL: *baseURL, Insecure: *insecure}, Mail: api.Mailpit{URL: *mailURL}}
	s := &seeder{p: p, rng: mrand.New(mrand.NewPCG(*seed, *seed^0x5eed)), password: *password, domain: *domain}
	if err := s.run(ctx, *admin, *adminPass, *institution, *count); err != nil {
		log.Fatal(err)
	}
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

type seeder struct {
	p        api.Platform
	rng      *mrand.Rand
	password string
	domain   string
}

func step(format string, args ...any) { fmt.Printf("• "+format+"\n", args...) }

func (s *seeder) run(ctx context.Context, admin, adminPass, name string, count int) error {
	rep, err := s.p.SignIn(ctx, admin, adminPass)
	if err != nil {
		return fmt.Errorf("secretariat sign-in: %w", err)
	}
	inst, err := api.EnsureInstitution(ctx, rep, name, "secretariat@"+s.domain)
	if err != nil {
		return fmt.Errorf("register institution: %w", err)
	}
	if need := len(courses) + 5 - inst.Credits; need > 0 {
		if inst.Credits, err = api.Purchase(ctx, rep, need); err != nil {
			return fmt.Errorf("purchase credits: %w", err)
		}
	}
	step("institution %q with %d credits", inst.Name, inst.Credits)

	students := s.students(count)
	roster := make([]api.RosterEntry, len(students))
	for i, st := range students {
		roster[i] = st.RosterEntry
	}
	if err := api.UploadRoster(ctx, rep, roster); err != nil {
		return fmt.Errorf("student registry: %w", err)
	}
	step("student registry with %d students", len(roster))

	instructors := make([]*api.Session, 3)
	for i := range instructors {
		email := fmt.Sprintf("instructor%d@%s", i+1, s.domain)
		if instructors[i], err = s.p.Instructor(ctx, rep, email, s.password); err != nil {
			return err
		}
	}
	step("%d instructors signed in", len(instructors))

	for i, st := range students {
		if st.session, err = s.p.Student(ctx, st.StudentID, st.Email, s.password); err != nil {
			return err
		}
		if (i+1)%10 == 0 || i+1 == len(students) {
			step("%d/%d students signed in (sign-ins are rate limited)", i+1, len(students))
		}
	}

	for _, c := range courses {
		if err := s.course(ctx, c, instructors[c.instructor], students); err != nil {
			return fmt.Errorf("course %s: %w", c.Code, err)
		}
	}
	if err := rep.JSON(ctx, http.MethodGet, "/institution", nil, &inst); err != nil {
		return err
	}
	fmt.Printf("\nDone. %d credits left.\n", inst.Credits)
	fmt.Printf("Accounts (password %s):\n  secretariat: %s\n", s.password, admin)
	for i := range instructors {
		fmt.Printf("  instructor:  instructor%d@%s\n", i+1, s.domain)
	}
	fmt.Printf("  students:    %s … %s\n", students[0].Email, students[len(students)-1].Email)
	return nil
}

func (s *seeder) students(count int) []*student {
	out := make([]*student, count)
	for i := range out {
		number := 21001 + i
		out[i] = &student{RosterEntry: api.RosterEntry{
			StudentID: fmt.Sprintf("0312%05d", number),
			Email:     fmt.Sprintf("el%05d@%s", number, s.domain),
			Name:      firstNames[s.rng.IntN(len(firstNames))] + " " + lastNames[s.rng.IntN(len(lastNames))],
		}}
	}
	return out
}

// course publishes the initial grades, files and answers review requests,
// and (for some courses) publishes the final grades.
func (s *seeder) course(ctx context.Context, c course, instructor *api.Session, students []*student) error {
	var enrolled []*student
	for _, st := range students {
		if s.rng.Float64() < 0.7 {
			enrolled = append(enrolled, st)
		}
	}
	rows := make([]workbook.Row, len(enrolled))
	for i, st := range enrolled {
		rows[i] = s.grade(c, st)
	}
	initial, err := s.publish(ctx, instructor, c, "initial", rows)
	if err != nil {
		return err
	}
	gradingID := initial.GradingID

	// Some students ask for a review once the grades are visible to them.
	askers := enrolled[:min(4, len(enrolled))]
	for _, st := range askers {
		msg := messages[s.rng.IntN(len(messages))]
		err := api.Eventually(ctx, 20*time.Second, func() error {
			_, err := api.RequestReview(ctx, st.session, gradingID, msg)
			if api.Is(err, http.StatusConflict) {
				return nil // already filed (re-run) or the grading is final
			}
			return err
		})
		if err != nil {
			return fmt.Errorf("review request by %s: %w", st.StudentID, err)
		}
	}
	// The instructor answers most of them; accepted requests change the
	// final grade.
	var inbox []api.Review
	if err := instructor.JSON(ctx, http.MethodGet, "/reviews/inbox", nil, &inbox); err != nil {
		return err
	}
	// One request stays pending, also when the seed runs again.
	answered := 0
	for _, r := range inbox {
		if r.GradingID == gradingID && r.Status != "pending" {
			answered++
		}
	}
	for _, r := range inbox {
		if r.GradingID != gradingID || r.Status != "pending" || answered >= len(askers)-1 {
			continue
		}
		action := actions[answered%len(actions)]
		if _, err := api.Reply(ctx, instructor, r.ID, action, replies[action]); err != nil {
			return fmt.Errorf("reply: %w", err)
		}
		answered++
		for i := range rows {
			if rows[i].StudentID == r.StudentID {
				rows[i].Total = math.Min(10, rows[i].Total+map[string]float64{"total_accept": 1, "partial_accept": 0.5}[action])
			}
		}
	}
	state := "open"
	if c.final {
		if _, err := s.publish(ctx, instructor, c, "final", rows); err != nil {
			return err
		}
		state = "final"
	}
	step("%s %s: %d students, %d review requests, %d answered, %s", c.Code, c.Title, len(rows), len(askers), answered, state)
	return nil
}

// publish uploads and confirms a workbook unless the grading already
// reached that stage.
func (s *seeder) publish(ctx context.Context, instructor *api.Session, c course, kind string, rows []workbook.Row) (api.Preview, error) {
	data, err := workbook.Build(c.Course, rows)
	if err != nil {
		return api.Preview{}, err
	}
	filename := strings.ToLower(fmt.Sprintf("%s-%s.xlsx", c.Code, kind))
	preview, err := api.UploadGrades(ctx, instructor, kind, filename, data)
	if err != nil {
		return preview, err
	}
	done := preview.CurrentState == "final" || (kind == "initial" && preview.CurrentState == "open")
	if done {
		_ = instructor.JSON(ctx, http.MethodPost, "/grades/uploads/"+preview.UploadID+"/cancel", nil, nil)
		return preview, nil
	}
	if !preview.CanConfirm {
		return preview, fmt.Errorf("%s upload rejected: %s", kind, strings.Join(preview.Problems, "; "))
	}
	if _, err := api.Confirm(ctx, instructor, preview.UploadID); err != nil {
		return preview, fmt.Errorf("confirm %s: %w", kind, err)
	}
	return preview, nil
}

// grade draws raw scores (0–10, some blank) and a total that is their
// weighted mean.
func (s *seeder) grade(c course, st *student) workbook.Row {
	scores := make([]*float64, len(c.Weights))
	var sum, weights float64
	skill := 3 + s.rng.Float64()*7
	for i, w := range c.Weights {
		weights += w
		if s.rng.Float64() < 0.05 {
			continue
		}
		v := math.Round(math.Max(0, math.Min(10, skill+s.rng.NormFloat64()*2)))
		scores[i] = &v
		sum += v * w
	}
	total := math.Round(sum/weights*100) / 100
	return workbook.Row{StudentID: st.StudentID, Name: st.Name, Email: st.Email, Total: total, Scores: scores}
}
