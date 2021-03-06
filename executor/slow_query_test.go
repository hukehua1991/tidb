// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package executor_test

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"time"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/executor"
	"github.com/pingcap/tidb/sessionctx"
	"github.com/pingcap/tidb/sessionctx/variable"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/util/logutil"
	"github.com/pingcap/tidb/util/mock"
)

func parseSlowLog(ctx sessionctx.Context, reader *bufio.Reader, checkValid func(string) bool) ([][]types.Datum, int, error) {
	rows, lineNum, err := executor.ParseSlowLog(ctx, reader, 0, 1024, checkValid)
	if err == io.EOF {
		err = nil
	}
	return rows, lineNum, err
}

func (s *testSuite) TestParseSlowLogFile(c *C) {
	slowLogStr :=
		`# Time: 2019-04-28T15:24:04.309074+08:00
# Txn_start_ts: 405888132465033227
# Query_time: 0.216905
# Process_time: 0.021 Request_count: 1 Total_keys: 637 Processed_keys: 436
# Is_internal: true
# Digest: 42a1c8aae6f133e934d4bf0147491709a8812ea05ff8819ec522780fe657b772
# Stats: t1:1,t2:2
# Cop_proc_avg: 0.1 Cop_proc_p90: 0.2 Cop_proc_max: 0.03 Cop_proc_addr: 127.0.0.1:20160
# Cop_wait_avg: 0.05 Cop_wait_p90: 0.6 Cop_wait_max: 0.8 Cop_wait_addr: 0.0.0.0:20160
# Mem_max: 70724
# Succ: false
# Plan_digest: 60e9378c746d9a2be1c791047e008967cf252eb6de9167ad3aa6098fa2d523f4
# Prev_stmt: update t set i = 1;
select * from t;`
	reader := bufio.NewReader(bytes.NewBufferString(slowLogStr))
	loc, err := time.LoadLocation("Asia/Shanghai")
	c.Assert(err, IsNil)
	s.ctx = mock.NewContext()
	s.ctx.GetSessionVars().TimeZone = loc
	rows, _, err := parseSlowLog(s.ctx, reader, func(_ string) bool { return false })
	c.Assert(err, IsNil)
	c.Assert(len(rows), Equals, 0)
	reader = bufio.NewReader(bytes.NewBufferString(slowLogStr))
	rows, _, err = parseSlowLog(s.ctx, reader, nil)
	c.Assert(err, IsNil)
	c.Assert(len(rows), Equals, 1)
	recordString := ""
	for i, value := range rows[0] {
		str, err := value.ToString()
		c.Assert(err, IsNil)
		if i > 0 {
			recordString += ","
		}
		recordString += str
	}
	expectRecordString := "2019-04-28 15:24:04.309074,405888132465033227,,,0,0.216905,0,0,0,0,0,0,0,,0,0,0,0,0,0,0.021,0,0,0,1,637,0,,,1,42a1c8aae6f133e934d4bf0147491709a8812ea05ff8819ec522780fe657b772,t1:1,t2:2,0.1,0.2,0.03,127.0.0.1:20160,0.05,0.6,0.8,0.0.0.0:20160,70724,0,,60e9378c746d9a2be1c791047e008967cf252eb6de9167ad3aa6098fa2d523f4,update t set i = 1;,select * from t;"
	c.Assert(expectRecordString, Equals, recordString)

	// fix sql contain '# ' bug
	slowLog := bytes.NewBufferString(
		`# Time: 2019-04-28T15:24:04.309074+08:00
select a# from t;
# Time: 2019-01-24T22:32:29.313255+08:00
# Txn_start_ts: 405888132465033227
# Query_time: 0.216905
# Process_time: 0.021 Request_count: 1 Total_keys: 637 Processed_keys: 436
# Is_internal: true
# Digest: 42a1c8aae6f133e934d4bf0147491709a8812ea05ff8819ec522780fe657b772
# Stats: t1:1,t2:2
# Succ: false
select * from t;
`)
	reader = bufio.NewReader(slowLog)
	_, _, err = parseSlowLog(s.ctx, reader, nil)
	c.Assert(err, IsNil)

	// test for time format compatibility.
	slowLog = bytes.NewBufferString(
		`# Time: 2019-04-28T15:24:04.309074+08:00
select * from t;
# Time: 2019-04-24-19:41:21.716221 +0800
select * from t;
`)
	reader = bufio.NewReader(slowLog)
	rows, _, err = parseSlowLog(s.ctx, reader, nil)
	c.Assert(err, IsNil)
	c.Assert(len(rows) == 2, IsTrue)
	t0Str, err := rows[0][0].ToString()
	c.Assert(err, IsNil)
	c.Assert(t0Str, Equals, "2019-04-28 15:24:04.309074")
	t1Str, err := rows[1][0].ToString()
	c.Assert(err, IsNil)
	c.Assert(t1Str, Equals, "2019-04-24 19:41:21.716221")

	// test for bufio.Scanner: token too long.
	slowLog = bytes.NewBufferString(
		`# Time: 2019-04-28T15:24:04.309074+08:00
select * from t;
# Time: 2019-04-24-19:41:21.716221 +0800
`)
	originValue := variable.MaxOfMaxAllowedPacket
	variable.MaxOfMaxAllowedPacket = 65536
	sql := strings.Repeat("x", int(variable.MaxOfMaxAllowedPacket+1))
	slowLog.WriteString(sql)
	reader = bufio.NewReader(slowLog)
	_, _, err = parseSlowLog(s.ctx, reader, nil)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Equals, "single line length exceeds limit: 65536")

	variable.MaxOfMaxAllowedPacket = originValue
	reader = bufio.NewReader(slowLog)
	_, _, err = parseSlowLog(s.ctx, reader, nil)
	c.Assert(err, IsNil)

	// Add parse error check.
	slowLog = bytes.NewBufferString(
		`# Time: 2019-04-28T15:24:04.309074+08:00
# Succ: abc
select * from t;
`)
	reader = bufio.NewReader(slowLog)
	_, _, err = parseSlowLog(s.ctx, reader, nil)
	c.Assert(err, IsNil)
	warnings := s.ctx.GetSessionVars().StmtCtx.GetWarnings()
	c.Assert(warnings, HasLen, 1)
	c.Assert(warnings[0].Err.Error(), Equals, "Parse slow log at line 2 failed. Field: `Succ`, error: strconv.ParseBool: parsing \"abc\": invalid syntax")
}

func (s *testSuite) TestSlowLogParseTime(c *C) {
	t1Str := "2019-01-24T22:32:29.313255+08:00"
	t2Str := "2019-01-24T22:32:29.313255"
	t1, err := executor.ParseTime(t1Str)
	c.Assert(err, IsNil)
	loc, err := time.LoadLocation("Asia/Shanghai")
	c.Assert(err, IsNil)
	t2, err := time.ParseInLocation("2006-01-02T15:04:05.999999999", t2Str, loc)
	c.Assert(err, IsNil)
	c.Assert(t1.Unix(), Equals, t2.Unix())
	t1Format := t1.In(loc).Format(logutil.SlowLogTimeFormat)
	c.Assert(t1Format, Equals, t1Str)
}

// TestFixParseSlowLogFile bugfix
// sql select * from INFORMATION_SCHEMA.SLOW_QUERY limit 1;
// ERROR 1105 (HY000): string "2019-05-12-11:23:29.61474688" doesn't has a prefix that matches format "2006-01-02-15:04:05.999999999 -0700", err: parsing time "2019-05-12-11:23:29.61474688" as "2006-01-02-15:04:05.999999999 -0700": cannot parse "" as "-0700"
func (s *testSuite) TestFixParseSlowLogFile(c *C) {
	slowLog := bytes.NewBufferString(
		`# Time: 2019-05-12-11:23:29.614327491 +0800
# Txn_start_ts: 405888132465033227
# Query_time: 0.216905
# Process_time: 0.021 Request_count: 1 Total_keys: 637 Processed_keys: 436
# Is_internal: true
# Digest: 42a1c8aae6f133e934d4bf0147491709a8812ea05ff8819ec522780fe657b772
# Stats: t1:1,t2:2
# Cop_proc_avg: 0.1 Cop_proc_p90: 0.2 Cop_proc_max: 0.03
# Cop_wait_avg: 0.05 Cop_wait_p90: 0.6 Cop_wait_max: 0.8
# Mem_max: 70724
select * from t
# Time: 2019-05-12-11:23:29.614327491 +0800
# Txn_start_ts: 405888132465033227
# Query_time: 0.216905
# Process_time: 0.021 Request_count: 1 Total_keys: 637 Processed_keys: 436
# Is_internal: true
# Digest: 42a1c8aae6f133e934d4bf0147491709a8812ea05ff8819ec522780fe657b772
# Stats: t1:1,t2:2
# Cop_proc_avg: 0.1 Cop_proc_p90: 0.2 Cop_proc_max: 0.03
# Cop_wait_avg: 0.05 Cop_wait_p90: 0.6 Cop_wait_max: 0.8
# Mem_max: 70724
# Plan_digest: 60e9378c746d9a2be1c791047e008967cf252eb6de9167ad3aa6098fa2d523f4
select * from t;`)
	scanner := bufio.NewReader(slowLog)
	loc, err := time.LoadLocation("Asia/Shanghai")
	c.Assert(err, IsNil)
	s.ctx = mock.NewContext()
	s.ctx.GetSessionVars().TimeZone = loc
	_, _, err = parseSlowLog(s.ctx, scanner, nil)
	c.Assert(err, IsNil)

	// Test parser error.
	slowLog = bytes.NewBufferString(
		`# Time: 2019-05-12-11:23:29.614327491 +0800
# Txn_start_ts: 405888132465033227#
`)

	scanner = bufio.NewReader(slowLog)
	_, _, err = parseSlowLog(s.ctx, scanner, nil)
	c.Assert(err, IsNil)
	warnings := s.ctx.GetSessionVars().StmtCtx.GetWarnings()
	c.Assert(warnings, HasLen, 1)
	c.Assert(warnings[0].Err.Error(), Equals, "Parse slow log at line 2 failed. Field: `Txn_start_ts`, error: strconv.ParseUint: parsing \"405888132465033227#\": invalid syntax")

}
