package monitor

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const CronDisabled = "#finallyshell-disabled# "

type CronJob struct {
	Line                    int
	Schedule, User, Command string
	Disabled                bool
}

var cronUser = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*\$?$`)
var cronEnvironment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*[ \t]*=`)

func ValidCronUser(user string) bool { return len(user) <= 64 && cronUser.MatchString(user) }

func CronJobs(text string, system bool) []CronJob {
	var rows []CronJob
	for index, line := range strings.Split(text, "\n") {
		body := strings.TrimSpace(line)
		body, disabled := strings.CutPrefix(body, CronDisabled)
		if body == "" || strings.HasPrefix(body, "#") || cronEnvironment.MatchString(body) {
			continue
		}
		count := 5
		if strings.HasPrefix(body, "@") {
			count = 1
		}
		var fields []string
		for range count {
			field, rest := cronField(body)
			fields, body = append(fields, field), rest
		}
		var user string
		if system {
			user, body = cronField(body)
		}
		if body != "" && (!system || ValidCronUser(user)) {
			rows = append(rows, CronJob{Line: index, Schedule: strings.Join(fields, " "), User: user, Command: body, Disabled: disabled})
		}
	}
	return rows
}

func cronField(text string) (string, string) {
	index := strings.IndexAny(text, " \t")
	if index < 0 {
		return text, ""
	}
	return text[:index], strings.TrimLeft(text[index:], " \t")
}

func ChangeCron(text string, index int, replacement string) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	switch {
	case index == -1 && replacement != "":
		lines = append(lines, replacement)
	case index >= 0 && index < len(lines):
		if replacement == "" {
			lines = append(lines[:index], lines[index+1:]...)
		} else {
			lines[index] = replacement
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func ToggleCron(text string, job CronJob) string {
	lines := strings.Split(text, "\n")
	if job.Line < 0 || job.Line >= len(lines) {
		return text
	}
	line := lines[job.Line]
	if job.Disabled {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, CronDisabled)
	} else {
		line = CronDisabled + line
	}
	return ChangeCron(text, job.Line, line)
}

type CronSchedule struct {
	Fields                    [5]uint64
	Reboot, dayStar, weekStar bool
}

var cronMonths = strings.Fields("JAN FEB MAR APR MAY JUN JUL AUG SEP OCT NOV DEC")
var cronWeekdays = strings.Fields("SUN MON TUE WED THU FRI SAT")

func ParseCronSchedule(schedule string) (CronSchedule, error) {
	var rule CronSchedule
	text := strings.TrimSpace(schedule)
	switch text {
	case "@reboot":
		rule.Reboot = true
		return rule, nil
	case "@yearly", "@annually":
		text = "0 0 1 1 *"
	case "@monthly":
		text = "0 0 1 * *"
	case "@weekly":
		text = "0 0 * * 0"
	case "@daily", "@midnight":
		text = "0 0 * * *"
	case "@hourly":
		text = "0 * * * *"
	}
	fields := strings.Fields(text)
	if len(fields) != 5 {
		return rule, errors.New("expected five cron fields")
	}
	mins, maxes := [...]int{0, 0, 1, 1, 0}, [...]int{59, 23, 31, 12, 7}
	for index, field := range fields {
		bits, ok := cronBits(strings.ToUpper(field), mins[index], maxes[index], index)
		if !ok {
			return rule, fmt.Errorf("invalid cron field %d", index+1)
		}
		rule.Fields[index] = bits
	}
	if rule.Fields[4]&(1<<7) != 0 {
		rule.Fields[4] |= 1
	}
	rule.dayStar, rule.weekStar = strings.HasPrefix(fields[2], "*"), strings.HasPrefix(fields[4], "*")
	return rule, nil
}

func cronBits(text string, floor, ceiling, field int) (uint64, bool) {
	var bits uint64
	for item := range strings.SplitSeq(text, ",") {
		base, stride, stepped := strings.Cut(item, "/")
		step := 1
		if stepped {
			step, _ = strconv.Atoi(stride)
			if step < 1 || step > ceiling-floor+1 || strings.Trim(stride, "0123456789") != "" {
				return 0, false
			}
		}
		low, high := floor, ceiling
		if base != "*" {
			start, end, ranged := strings.Cut(base, "-")
			low = cronNumber(start, field)
			if ranged {
				high = cronNumber(end, field)
			} else if stepped {
				return 0, false
			} else {
				high = low
			}
		}
		if low < floor || high > ceiling || high < low {
			return 0, false
		}
		for value := low; value <= high; value += step {
			bits |= 1 << value
		}
	}
	return bits, bits != 0
}

func cronNumber(text string, field int) int {
	if text != "" && strings.Trim(text, "0123456789") == "" {
		if number, err := strconv.Atoi(text); err == nil {
			return number
		}
	}
	if field == 3 {
		for index, name := range cronMonths {
			if name == text {
				return index + 1
			}
		}
	}
	if field == 4 {
		for index, name := range cronWeekdays {
			if name == text {
				return index
			}
		}
	}
	return -1
}

func (rule CronSchedule) Next(after time.Time, count int) []time.Time {
	if rule.Reboot || count < 1 {
		return nil
	}
	var times []time.Time
	limit := after.AddDate(12, 0, 0)
	for next := after.Truncate(time.Minute).Add(time.Minute); next.Before(limit); {
		month := rule.Fields[3]&(1<<next.Month()) != 0
		day := rule.Fields[2]&(1<<next.Day()) != 0
		week := rule.Fields[4]&(1<<next.Weekday()) != 0
		matchDay := day || week
		if rule.dayStar || rule.weekStar {
			matchDay = day && week
		}
		switch {
		case !month || !matchDay:
			next = time.Date(next.Year(), next.Month(), next.Day()+1, 0, 0, 0, 0, next.Location())
			continue
		case rule.Fields[1]&(1<<next.Hour()) == 0:
			next = next.Add(time.Duration(60-next.Minute()) * time.Minute)
			continue
		case rule.Fields[0]&(1<<next.Minute()) != 0:
			times = append(times, next)
			if len(times) == count {
				return times
			}
		}
		next = next.Add(time.Minute)
	}
	return times
}

func CronZone(text string) (*time.Location, error) {
	text = strings.TrimSpace(text)
	if len(text) != 5 || text[0] != '+' && text[0] != '-' || strings.Trim(text[1:], "0123456789") != "" {
		return nil, errors.New("invalid server UTC offset")
	}
	hour, _ := strconv.Atoi(text[1:3])
	minute, _ := strconv.Atoi(text[3:])
	if hour > 23 || minute > 59 {
		return nil, errors.New("invalid server UTC offset")
	}
	offset := (hour*60 + minute) * 60
	if text[0] == '-' {
		offset = -offset
	}
	return time.FixedZone("UTC"+text, offset), nil
}
