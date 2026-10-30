package resident

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func fmtTimes(ts []time.Time, loc string) string {
	l, _ := time.LoadLocation(loc)
	var out []string
	for _, t := range ts {
		out = append(out, t.In(l).Format("2006-01-02 15:04 MST"))
	}
	return strings.Join(out, ", ")
}

func TestScheduleNext(t *testing.T) {
	cases := []struct {
		name  string
		s     Schedule
		after string
		want  string
	}{
		{"毎日7時の次回3回をAsia/Tokyoで計算する", Schedule{Kind: ScheduleDaily, Time: "07:00"}, "2026-10-30T07:30:00+09:00",
			"2026-10-31 07:00 JST, 2026-11-01 07:00 JST, 2026-11-02 07:00 JST"},
		{"毎日の時刻ちょうどは含めず次の日にする", Schedule{Kind: ScheduleDaily, Time: "07:00"}, "2026-10-30T07:00:00+09:00",
			"2026-10-31 07:00 JST, 2026-11-01 07:00 JST, 2026-11-02 07:00 JST"},
		{"毎週は指定曜日だけ", Schedule{Kind: ScheduleWeekly, Time: "09:15", Days: []string{"MON", "thursday"}}, "2026-10-30T00:00:00+09:00",
			"2026-11-02 09:15 JST, 2026-11-05 09:15 JST, 2026-11-09 09:15 JST"},
		{"cronの範囲とステップと曜日名", Schedule{Kind: ScheduleCron, Cron: "*/30 8-9 * * mon-fri"}, "2026-10-30T09:10:00+09:00",
			"2026-10-30 09:30 JST, 2026-11-02 08:00 JST, 2026-11-02 08:30 JST"},
		{"cronの日と曜日が両方指定ならどちらかで実行する", Schedule{Kind: ScheduleCron, Cron: "0 12 1 * 0"}, "2026-10-30T00:00:00+09:00",
			"2026-11-01 12:00 JST, 2026-11-08 12:00 JST, 2026-11-15 12:00 JST"},
		{"cronの曜日7は日曜日", Schedule{Kind: ScheduleCron, Cron: "0 6 * * 7"}, "2026-10-30T00:00:00+09:00",
			"2026-11-01 06:00 JST, 2026-11-08 06:00 JST, 2026-11-15 06:00 JST"},
		{"timezoneを指定するとその地域の壁時計で解釈する", Schedule{Kind: ScheduleDaily, Time: "07:00", Timezone: "Europe/London"}, "2026-10-30T00:00:00Z",
			"2026-10-30 07:00 GMT, 2026-10-31 07:00 GMT, 2026-11-01 07:00 GMT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := c.s.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			got := fmtTimes(s.NextN(mustTime(t, c.after), 3), s.Timezone)
			if got != c.want {
				t.Errorf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestScheduleDST(t *testing.T) {
	t.Run("夏時間で存在しない時刻はずれの分だけ後ろにずらす", func(t *testing.T) {
		s := Schedule{Kind: ScheduleDaily, Time: "02:30", Timezone: "America/New_York"}
		got := fmtTimes(s.NextN(mustTime(t, "2026-03-07T12:00:00Z"), 3), "America/New_York")
		want := "2026-03-08 03:30 EDT, 2026-03-09 02:30 EDT, 2026-03-10 02:30 EDT"
		if got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})
	t.Run("夏時間で重複する時刻は最初の1回だけ実行する", func(t *testing.T) {
		s := Schedule{Kind: ScheduleDaily, Time: "01:30", Timezone: "America/New_York"}
		got := fmtTimes(s.NextN(mustTime(t, "2026-10-31T12:00:00Z"), 3), "America/New_York")
		want := "2026-11-01 01:30 EDT, 2026-11-02 01:30 EST, 2026-11-03 01:30 EST"
		if got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})
	t.Run("cronでも重複する時刻は1回で、ずらした時刻と重ならない", func(t *testing.T) {
		s := Schedule{Kind: ScheduleCron, Cron: "30 1,2,3 * * *", Timezone: "America/New_York"}
		got := fmtTimes(s.NextN(mustTime(t, "2026-03-08T00:00:00Z"), 3), "America/New_York")
		// 02:30 does not exist on 03-08 and becomes 03:30, the same instant as the 03:30 slot.
		want := "2026-03-08 01:30 EST, 2026-03-08 03:30 EDT, 2026-03-09 01:30 EDT"
		if got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})
	t.Run("毎朝7時は起点からの24時間間隔ではなく壁時計の7時", func(t *testing.T) {
		s := Schedule{Kind: ScheduleDaily, Time: "07:00", Timezone: "America/New_York"}
		ts := s.NextN(mustTime(t, "2026-10-30T12:00:00Z"), 2)
		if d := ts[1].Sub(ts[0]); d != 25*time.Hour {
			t.Errorf("across the DST change the gap is %s, want 25h", d)
		}
	})
}

func TestScheduleValidation(t *testing.T) {
	at := mustTime(t, "2026-11-01T07:00:00+09:00")
	cases := []struct {
		name string
		s    Schedule
		want string
	}{
		{"cronのフィールド数違いを拒否する", Schedule{Kind: ScheduleCron, Cron: "0 7 * *"}, "5 fields"},
		{"cronの範囲外の値を拒否する", Schedule{Kind: ScheduleCron, Cron: "0 25 * * *"}, "hour \"25\" is out of range"},
		{"cronの逆向きの範囲を拒否する", Schedule{Kind: ScheduleCron, Cron: "0 9-7 * * *"}, "backwards"},
		{"未知のtimezoneを拒否する", Schedule{Kind: ScheduleDaily, Time: "07:00", Timezone: "Asia/Nowhere"}, "unknown timezone"},
		{"Localのtimezoneは使わせない", Schedule{Kind: ScheduleDaily, Time: "07:00", Timezone: "Local"}, "unknown timezone"},
		{"時刻の書式違いを拒否する", Schedule{Kind: ScheduleDaily, Time: "7:0"}, "HH:MM"},
		{"曜日なしの毎週を拒否する", Schedule{Kind: ScheduleWeekly, Time: "07:00"}, "at least one day"},
		{"未知の種類を拒否する", Schedule{Kind: "hourly"}, "unknown kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.s.Normalize()
			if err == nil || !errors.Is(err, errSchedule) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
	t.Run("一回限りは過去なら次回がない", func(t *testing.T) {
		s := Schedule{Kind: ScheduleOnce, At: &at}
		if _, ok := s.Next(at.Add(time.Minute)); ok {
			t.Error("a past one-off still has a next run")
		}
		if n, ok := s.Next(at.Add(-time.Hour)); !ok || !n.Equal(at) {
			t.Errorf("next = %v %v", n, ok)
		}
	})
	t.Run("timezoneの既定はAsia/Tokyo", func(t *testing.T) {
		s, err := Schedule{Kind: ScheduleManual}.Normalize()
		if err != nil || s.Timezone != "Asia/Tokyo" {
			t.Errorf("%+v %v", s, err)
		}
	})
}
