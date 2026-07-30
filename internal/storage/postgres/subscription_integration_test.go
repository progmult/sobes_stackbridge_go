// Интеграционные тесты хранилища: расчёт суммы за период живёт в SQL, поэтому
// заглушками его не проверить. Тесты поднимают настоящий PostgreSQL в
// контейнере и накатывают те же миграции, что и сервис при старте.
//
// Если docker недоступен, тесты пропускаются.
package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"sobes_stackbridge_go/internal/config"
	"sobes_stackbridge_go/internal/model"
	"sobes_stackbridge_go/internal/storage/postgres"
)

const (
	containerStartupTimeout = 2 * time.Minute
	queryTimeout            = 10 * time.Second
)

// Контейнер один на пакет: изоляцию между тестами даёт очистка таблицы в
// newRepository. Пустой skipReason означает, что база готова.
var (
	repo       *postgres.SubscriptionRepository
	pool       *pgxpool.Pool
	skipReason string
)

func TestMain(m *testing.M) {
	code, err := runTests(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	os.Exit(code)
}

// runTests поднимает контейнер, накатывает миграции и запускает тесты.
// Отдельная функция нужна, чтобы defer отработал до os.Exit.
func runTests(m *testing.M) (int, error) {
	ctx := context.Background()

	if err := dockerAvailable(ctx); err != nil {
		skipReason = "docker недоступен: " + err.Error()

		return m.Run(), nil
	}

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("subscriptions"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			// Postgres в образе стартует дважды: для initdb и на приём
			// соединений. Ждём второе сообщение, иначе попадём в середину
			// инициализации.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(containerStartupTimeout),
		),
	)
	if err != nil {
		return 0, fmt.Errorf("не удалось запустить postgres в контейнере: %w", err)
	}

	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintln(os.Stderr, "не удалось остановить контейнер:", err)
		}
	}()

	cfg, err := containerConfig(ctx, container)
	if err != nil {
		return 0, err
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Миграции накатываются тем же кодом, что и в сервисе, поэтому тесты
	// заодно проверяют, что схема применяется на чистую базу.
	if err := postgres.Migrate(ctx, cfg, log); err != nil {
		return 0, fmt.Errorf("не удалось применить миграции: %w", err)
	}

	pool, err = postgres.NewPool(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("не удалось подключиться к базе: %w", err)
	}
	defer pool.Close()

	repo = postgres.NewSubscriptionRepository(pool)

	return m.Run(), nil
}

// dockerAvailable отличает отсутствующий docker от сломанного контейнера:
// в первом случае тесты пропускаются, во втором должны падать.
func dockerAvailable(ctx context.Context) error {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return err
	}
	defer provider.Close()

	return provider.Health(ctx)
}

func containerConfig(ctx context.Context, container *tcpostgres.PostgresContainer) (*config.Config, error) {
	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить адрес контейнера: %w", err)
	}

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, fmt.Errorf("не удалось получить порт контейнера: %w", err)
	}

	return &config.Config{
		PostgresHost:     host,
		PostgresPort:     port.Port(),
		PostgresUser:     "postgres",
		PostgresPassword: "postgres",
		PostgresDB:       "subscriptions",
		PostgresSSLMode:  "disable",
	}, nil
}

// newRepository отдаёт хранилище с пустой таблицей.
func newRepository(t *testing.T) *postgres.SubscriptionRepository {
	t.Helper()

	if skipReason != "" {
		t.Skip(skipReason)
	}

	ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
	defer cancel()

	if _, err := pool.Exec(ctx, "TRUNCATE subscriptions"); err != nil {
		t.Fatalf("не удалось очистить таблицу: %v", err)
	}

	return repo
}

// date собирает первое число месяца — так подписки хранятся в базе.
func date(month time.Month, year int) time.Time {
	return time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
}

// fixture — подписка для наполнения таблицы. Указатель на дату окончания в
// литерале не запишешь, поэтому месяц и год окончания задаются отдельно,
// а нулевой год означает бессрочную подписку.
type fixture struct {
	serviceName string
	price       int
	userID      uuid.UUID
	startMonth  time.Month
	startYear   int
	endMonth    time.Month
	endYear     int
}

func (f fixture) subscription() *model.Subscription {
	sub := &model.Subscription{
		ServiceName: f.serviceName,
		Price:       f.price,
		UserID:      f.userID,
		StartDate:   date(f.startMonth, f.startYear),
	}

	if f.endYear != 0 {
		endDate := date(f.endMonth, f.endYear)
		sub.EndDate = &endDate
	}

	return sub
}

func seed(t *testing.T, r *postgres.SubscriptionRepository, fixtures ...fixture) []model.Subscription {
	t.Helper()

	created := make([]model.Subscription, 0, len(fixtures))

	for _, f := range fixtures {
		ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)

		sub, err := r.Create(ctx, f.subscription())
		cancel()

		if err != nil {
			t.Fatalf("не удалось создать подписку %q: %v", f.serviceName, err)
		}

		created = append(created, *sub)
	}

	return created
}

var (
	alice = uuid.MustParse("60601fee-2bf1-4721-ae6f-7636e79a0cba")
	bob   = uuid.MustParse("2f8b1f6e-8a37-4a2e-9f7a-1b9a0c7d5e31")
)

// TestSumForPeriodCountsMonths проверяет главное: сумма считается как
// месячная стоимость, умноженная на число месяцев пересечения срока подписки
// с запрошенным периодом.
func TestSumForPeriodCountsMonths(t *testing.T) {
	tests := []struct {
		name      string
		fixtures  []fixture
		from, to  time.Time
		wantTotal int64
	}{
		{
			name: "период в один месяц",
			fixtures: []fixture{
				{serviceName: "Yandex Plus", price: 400, userID: alice, startMonth: time.July, startYear: 2025},
			},
			from:      date(time.July, 2025),
			to:        date(time.July, 2025),
			wantTotal: 400,
		},
		{
			name: "подписка целиком внутри периода",
			fixtures: []fixture{
				{
					serviceName: "Yandex Plus", price: 400, userID: alice,
					startMonth: time.March, startYear: 2025,
					endMonth: time.May, endYear: 2025,
				},
			},
			from:      date(time.January, 2025),
			to:        date(time.December, 2025),
			wantTotal: 1200, // март, апрель, май
		},
		{
			name: "период целиком внутри срока подписки",
			fixtures: []fixture{
				{
					serviceName: "Yandex Plus", price: 400, userID: alice,
					startMonth: time.January, startYear: 2024,
					endMonth: time.December, endYear: 2026,
				},
			},
			from:      date(time.April, 2025),
			to:        date(time.June, 2025),
			wantTotal: 1200, // считаются только три месяца периода
		},
		{
			name: "подписка началась до периода",
			fixtures: []fixture{
				{
					serviceName: "Yandex Plus", price: 400, userID: alice,
					startMonth: time.November, startYear: 2024,
					endMonth: time.February, endYear: 2025,
				},
			},
			from:      date(time.January, 2025),
			to:        date(time.December, 2025),
			wantTotal: 800, // январь и февраль
		},
		{
			name: "подписка кончается после периода",
			fixtures: []fixture{
				{
					serviceName: "Yandex Plus", price: 400, userID: alice,
					startMonth: time.November, startYear: 2025,
					endMonth: time.March, endYear: 2026,
				},
			},
			from:      date(time.January, 2025),
			to:        date(time.December, 2025),
			wantTotal: 800, // ноябрь и декабрь
		},
		{
			name: "бессрочная подписка считается до конца периода",
			fixtures: []fixture{
				{serviceName: "Yandex Plus", price: 400, userID: alice, startMonth: time.October, startYear: 2025},
			},
			from:      date(time.January, 2025),
			to:        date(time.December, 2025),
			wantTotal: 1200, // октябрь, ноябрь, декабрь
		},
		{
			name: "период через границу года",
			fixtures: []fixture{
				{serviceName: "Yandex Plus", price: 400, userID: alice, startMonth: time.January, startYear: 2020},
			},
			from:      date(time.December, 2024),
			to:        date(time.January, 2025),
			wantTotal: 800,
		},
		{
			name: "подписка закончилась до периода",
			fixtures: []fixture{
				{
					serviceName: "Yandex Plus", price: 400, userID: alice,
					startMonth: time.January, startYear: 2024,
					endMonth: time.December, endYear: 2024,
				},
			},
			from:      date(time.January, 2025),
			to:        date(time.December, 2025),
			wantTotal: 0,
		},
		{
			name: "подписка начнётся после периода",
			fixtures: []fixture{
				{serviceName: "Yandex Plus", price: 400, userID: alice, startMonth: time.January, startYear: 2026},
			},
			from:      date(time.January, 2025),
			to:        date(time.December, 2025),
			wantTotal: 0,
		},
		{
			name:      "пустая таблица даёт ноль, а не ошибку",
			from:      date(time.January, 2025),
			to:        date(time.December, 2025),
			wantTotal: 0,
		},
		{
			name: "несколько подписок складываются",
			fixtures: []fixture{
				{
					serviceName: "Yandex Plus", price: 400, userID: alice,
					startMonth: time.January, startYear: 2025,
					endMonth: time.March, endYear: 2025,
				},
				{serviceName: "Netflix", price: 1000, userID: bob, startMonth: time.February, startYear: 2025},
			},
			from:      date(time.January, 2025),
			to:        date(time.March, 2025),
			wantTotal: 1200 + 2000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRepository(t)
			seed(t, r, tt.fixtures...)

			ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
			defer cancel()

			total, err := r.SumForPeriod(ctx, tt.from, tt.to, model.Filter{})
			if err != nil {
				t.Fatalf("SumForPeriod() вернул неожиданную ошибку: %v", err)
			}

			if total != tt.wantTotal {
				t.Errorf("SumForPeriod() = %d, ожидалось %d", total, tt.wantTotal)
			}
		})
	}
}

// TestSumForPeriodFilters проверяет фильтры из ТЗ: по пользователю и по
// названию подписки, по отдельности и вместе.
func TestSumForPeriodFilters(t *testing.T) {
	serviceName := "Yandex Plus"
	otherName := "Netflix"

	tests := []struct {
		name      string
		filter    model.Filter
		wantTotal int64
	}{
		{name: "без фильтров", wantTotal: 1200 + 3000 + 6000},
		{name: "по пользователю", filter: model.Filter{UserID: &alice}, wantTotal: 1200 + 3000},
		{name: "по названию сервиса", filter: model.Filter{ServiceName: &serviceName}, wantTotal: 1200 + 6000},
		{
			name:      "по пользователю и названию",
			filter:    model.Filter{UserID: &alice, ServiceName: &serviceName},
			wantTotal: 1200,
		},
		{
			name:      "фильтры не пересекаются",
			filter:    model.Filter{UserID: &bob, ServiceName: &otherName},
			wantTotal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRepository(t)
			seed(t, r,
				fixture{serviceName: serviceName, price: 400, userID: alice, startMonth: time.January, startYear: 2025},
				fixture{serviceName: otherName, price: 1000, userID: alice, startMonth: time.January, startYear: 2025},
				fixture{serviceName: serviceName, price: 2000, userID: bob, startMonth: time.January, startYear: 2025},
			)

			ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
			defer cancel()

			total, err := r.SumForPeriod(ctx, date(time.January, 2025), date(time.March, 2025), tt.filter)
			if err != nil {
				t.Fatalf("SumForPeriod() вернул неожиданную ошибку: %v", err)
			}

			if total != tt.wantTotal {
				t.Errorf("SumForPeriod() = %d, ожидалось %d", total, tt.wantTotal)
			}
		})
	}
}

// TestSumForPeriodIgnoresServiceNameCase закрепляет регистронезависимое
// сравнение: под него заточен индекс по lower(service_name).
func TestSumForPeriodIgnoresServiceNameCase(t *testing.T) {
	r := newRepository(t)
	seed(t, r, fixture{serviceName: "Yandex Plus", price: 400, userID: alice, startMonth: time.January, startYear: 2025})

	name := "yandex plus"

	ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
	defer cancel()

	total, err := r.SumForPeriod(ctx, date(time.January, 2025), date(time.January, 2025), model.Filter{ServiceName: &name})
	if err != nil {
		t.Fatalf("SumForPeriod() вернул неожиданную ошибку: %v", err)
	}

	if total != 400 {
		t.Errorf("SumForPeriod() = %d, ожидалось %d", total, 400)
	}
}

// TestRepositoryRoundTrip проверяет остальной SQL хранилища: запись
// возвращается той же, обновляется целиком и после удаления исчезает.
func TestRepositoryRoundTrip(t *testing.T) {
	r := newRepository(t)

	created := seed(t, r, fixture{
		serviceName: "Yandex Plus", price: 400, userID: alice,
		startMonth: time.July, startYear: 2025,
		endMonth: time.December, endYear: 2025,
	})[0]

	ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
	defer cancel()

	if created.ID == uuid.Nil {
		t.Fatal("Create() не вернул сгенерированный идентификатор")
	}

	got, err := r.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() вернул неожиданную ошибку: %v", err)
	}

	if got.ServiceName != "Yandex Plus" || got.Price != 400 || got.UserID != alice {
		t.Errorf("GetByID() = %+v, ожидались поля исходной подписки", got)
	}

	if !got.StartDate.Equal(date(time.July, 2025)) {
		t.Errorf("StartDate = %v, ожидалось %v", got.StartDate, date(time.July, 2025))
	}

	if got.EndDate == nil || !got.EndDate.Equal(date(time.December, 2025)) {
		t.Errorf("EndDate = %v, ожидалось %v", got.EndDate, date(time.December, 2025))
	}

	// PUT перезаписывает запись целиком, в том числе снимает дату окончания.
	updated := *got
	updated.Price = 500
	updated.EndDate = nil

	if _, err := r.Update(ctx, &updated); err != nil {
		t.Fatalf("Update() вернул неожиданную ошибку: %v", err)
	}

	got, err = r.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() после Update() вернул неожиданную ошибку: %v", err)
	}

	if got.Price != 500 || got.EndDate != nil {
		t.Errorf("после Update() = %+v, ожидались price=500 и end_date=nil", got)
	}

	if err := r.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() вернул неожиданную ошибку: %v", err)
	}

	if _, err := r.GetByID(ctx, created.ID); !isNotFound(err) {
		t.Errorf("GetByID() после удаления вернул ошибку %v, ожидалась ErrNotFound", err)
	}

	if err := r.Delete(ctx, created.ID); !isNotFound(err) {
		t.Errorf("повторный Delete() вернул ошибку %v, ожидалась ErrNotFound", err)
	}

	if _, err := r.Update(ctx, &updated); !isNotFound(err) {
		t.Errorf("Update() удалённой подписки вернул ошибку %v, ожидалась ErrNotFound", err)
	}
}

// TestNoOverlappingSubscriptions проверяет ограничение subscriptions_no_overlap
// на настоящей базе: правило живёт в схеме, а не в Go, и заглушками его не
// проверить.
func TestNoOverlappingSubscriptions(t *testing.T) {
	// Уже сохранённая подписка: с января, бессрочная.
	existing := fixture{serviceName: "Yandex Plus", price: 400, userID: alice, startMonth: time.January, startYear: 2025}

	tests := []struct {
		name         string
		candidate    fixture
		wantConflict bool
	}{
		{
			name: "тот же сервис за пересекающийся период",
			candidate: fixture{
				serviceName: "Yandex Plus", price: 400, userID: alice,
				startMonth: time.May, startYear: 2025,
			},
			wantConflict: true,
		},
		{
			name: "название в другом регистре — тот же сервис",
			candidate: fixture{
				serviceName: "yandex plus", price: 400, userID: alice,
				startMonth: time.May, startYear: 2025,
			},
			wantConflict: true,
		},
		{
			name: "период до начала существующей",
			candidate: fixture{
				serviceName: "Yandex Plus", price: 400, userID: alice,
				startMonth: time.October, startYear: 2024,
				endMonth: time.December, endYear: 2024,
			},
		},
		{
			name: "другой сервис",
			candidate: fixture{
				serviceName: "Netflix", price: 1000, userID: alice,
				startMonth: time.May, startYear: 2025,
			},
		},
		{
			name: "другой пользователь",
			candidate: fixture{
				serviceName: "Yandex Plus", price: 400, userID: bob,
				startMonth: time.May, startYear: 2025,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRepository(t)
			seed(t, r, existing)

			ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
			defer cancel()

			_, err := r.Create(ctx, tt.candidate.subscription())

			if tt.wantConflict {
				if !errors.Is(err, model.ErrConflict) {
					t.Fatalf("Create() вернул ошибку %v, ожидалась ErrConflict", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Create() вернул неожиданную ошибку: %v", err)
			}
		})
	}
}

// TestResubscriptionIsAllowed: закрытая подписка и следующая за ней — законный
// сценарий, ограничение не должно его запрещать. Проверяется и стык месяцев.
func TestResubscriptionIsAllowed(t *testing.T) {
	r := newRepository(t)
	seed(t, r, fixture{
		serviceName: "Yandex Plus", price: 400, userID: alice,
		startMonth: time.January, startYear: 2025,
		endMonth: time.April, endYear: 2025,
	})

	ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
	defer cancel()

	// Май — следующий месяц после апреля, пересечения нет.
	next := fixture{serviceName: "Yandex Plus", price: 500, userID: alice, startMonth: time.May, startYear: 2025}
	if _, err := r.Create(ctx, next.subscription()); err != nil {
		t.Fatalf("переподписка с мая отклонена: %v", err)
	}

	// А апрель входит в закрытый период: end_date включительный.
	april := fixture{
		serviceName: "Yandex Plus", price: 400, userID: alice,
		startMonth: time.April, startYear: 2025,
		endMonth: time.April, endYear: 2025,
	}
	if _, err := r.Create(ctx, april.subscription()); !errors.Is(err, model.ErrConflict) {
		t.Errorf("Create() за апрель вернул %v, ожидалась ErrConflict", err)
	}
}

// TestUpdateDoesNotConflictWithItself — место, где схема с EXCLUDE обычно и
// ломается: запись сравнивается сама с собой и любое обновление упирается в
// собственный период. README обещает идемпотентность PUT, поэтому случай
// закреплён отдельно.
func TestUpdateDoesNotConflictWithItself(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.Subscription)
	}{
		{name: "те же значения", mutate: func(*model.Subscription) {}},
		{name: "другая стоимость", mutate: func(s *model.Subscription) { s.Price = 500 }},
		{
			name: "период расширен",
			mutate: func(s *model.Subscription) {
				end := date(time.December, 2025)
				s.EndDate = &end
			},
		},
		{
			name:   "начало сдвинуто",
			mutate: func(s *model.Subscription) { s.StartDate = date(time.February, 2025) },
		},
		{name: "подписка стала бессрочной", mutate: func(s *model.Subscription) { s.EndDate = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRepository(t)
			created := seed(t, r, fixture{
				serviceName: "Yandex Plus", price: 400, userID: alice,
				startMonth: time.January, startYear: 2025,
				endMonth: time.June, endYear: 2025,
			})[0]

			ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
			defer cancel()

			updated := created
			tt.mutate(&updated)

			if _, err := r.Update(ctx, &updated); err != nil {
				t.Errorf("Update() вернул ошибку: %v", err)
			}
		})
	}
}

// TestConflictMessageHasNoStoragePrefix: наружу уходит формулировка о состоянии,
// а не о том, какую операцию не смог выполнить сервер. Для ErrNotFound это уже
// так, конфликт не должен выбиваться.
func TestConflictMessageHasNoStoragePrefix(t *testing.T) {
	existing := fixture{serviceName: "Yandex Plus", price: 400, userID: alice, startMonth: time.January, startYear: 2025}
	overlapping := fixture{serviceName: "Yandex Plus", price: 400, userID: alice, startMonth: time.May, startYear: 2025}

	r := newRepository(t)
	created := seed(t, r, existing)[0]

	ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
	defer cancel()

	_, err := r.Create(ctx, overlapping.subscription())
	if err == nil {
		t.Fatal("Create() пересекающейся подписки не вернул ошибку")
	}

	if err.Error() != model.ErrConflict.Error() {
		t.Errorf("сообщение Create() = %q, ожидалось %q", err, model.ErrConflict)
	}

	// То же самое на обновлении: там префикс был бы «не удалось обновить».
	conflicting := created
	conflicting.ID = seed(t, r, fixture{
		serviceName: "Netflix", price: 1000, userID: alice, startMonth: time.May, startYear: 2025,
	})[0].ID
	conflicting.ServiceName = "Yandex Plus"
	conflicting.StartDate = date(time.May, 2025)

	if _, err := r.Update(ctx, &conflicting); err.Error() != model.ErrConflict.Error() {
		t.Errorf("сообщение Update() = %q, ожидалось %q", err, model.ErrConflict)
	}
}

// TestUpdateIntoOverlapConflicts: ограничение действует и на обновление, при
// этом запись не конфликтует сама с собой.
func TestUpdateIntoOverlapConflicts(t *testing.T) {
	r := newRepository(t)
	created := seed(t, r,
		fixture{
			serviceName: "Yandex Plus", price: 400, userID: alice,
			startMonth: time.January, startYear: 2025,
			endMonth: time.April, endYear: 2025,
		},
		fixture{
			serviceName: "Yandex Plus", price: 400, userID: alice,
			startMonth: time.June, startYear: 2025,
			endMonth: time.August, endYear: 2025,
		},
	)

	ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
	defer cancel()

	// Сдвигаем вторую запись на май — пересечения с первой всё ещё нет.
	second := created[1]
	mayStart := date(time.May, 2025)
	second.StartDate = mayStart

	if _, err := r.Update(ctx, &second); err != nil {
		t.Fatalf("Update() без пересечения вернул ошибку: %v", err)
	}

	// А сдвиг на март наезжает на первую запись.
	second.StartDate = date(time.March, 2025)
	if _, err := r.Update(ctx, &second); !errors.Is(err, model.ErrConflict) {
		t.Errorf("Update() с пересечением вернул %v, ожидалась ErrConflict", err)
	}
}

// TestListFiltersAndPagination проверяет выборку списка: общее количество
// считается по фильтру, а не по странице.
func TestListFiltersAndPagination(t *testing.T) {
	r := newRepository(t)
	seed(t, r,
		fixture{serviceName: "Yandex Plus", price: 400, userID: alice, startMonth: time.January, startYear: 2025},
		fixture{serviceName: "Netflix", price: 1000, userID: alice, startMonth: time.February, startYear: 2025},
		fixture{serviceName: "Spotify", price: 300, userID: alice, startMonth: time.March, startYear: 2025},
		fixture{serviceName: "Netflix", price: 1000, userID: bob, startMonth: time.April, startYear: 2025},
	)

	ctx, cancel := context.WithTimeout(t.Context(), queryTimeout)
	defer cancel()

	items, total, err := r.List(ctx, model.Filter{UserID: &alice}, model.Page{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("List() вернул неожиданную ошибку: %v", err)
	}

	if total != 3 {
		t.Errorf("total = %d, ожидалось 3: количество считается по фильтру, а не по странице", total)
	}

	if len(items) != 2 {
		t.Fatalf("len(items) = %d, ожидалось 2", len(items))
	}

	// Сортировка по дате начала по убыванию: сначала март, затем февраль.
	if items[0].ServiceName != "Spotify" || items[1].ServiceName != "Netflix" {
		t.Errorf("порядок = %q, %q, ожидался Spotify, Netflix", items[0].ServiceName, items[1].ServiceName)
	}

	items, total, err = r.List(ctx, model.Filter{UserID: &alice}, model.Page{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List() со смещением вернул неожиданную ошибку: %v", err)
	}

	if total != 3 || len(items) != 1 || items[0].ServiceName != "Yandex Plus" {
		t.Errorf("вторая страница = %+v (total %d), ожидалась одна запись Yandex Plus", items, total)
	}

	items, _, err = r.List(ctx, model.Filter{}, model.Page{Limit: 10, Offset: 100})
	if err != nil {
		t.Fatalf("List() за последней страницей вернул неожиданную ошибку: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("за последней страницей вернулось %d записей, ожидалось 0", len(items))
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, model.ErrNotFound)
}
