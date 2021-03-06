/*
Rating system designed to be used in VoIP Carriers World
Copyright (C) 2013 ITsysCOM

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>
*/

package sessionmanager

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cgrates/cgrates/engine"
	"github.com/cgrates/cgrates/utils"
	"github.com/cgrates/fsock"
)

// Session type holding the call information fields, a session delegate for specific
// actions and a channel to signal end of the debit loop.
type Session struct {
	cgrid          string
	uuid           string
	stopDebit      chan bool
	sessionManager SessionManager
	sessionRuns    []*SessionRun
}

func (s *Session) GetSessionRun(runid string) *SessionRun {
	for _, sr := range s.sessionRuns {
		if sr.runId == runid {
			return sr
		}
	}
	return nil
}

// One individual run
type SessionRun struct {
	runId          string
	callDescriptor *engine.CallDescriptor
	callCosts      []*engine.CallCost
}

// Creates a new session and in case of prepaid starts the debit loop for each of the session runs individually
func NewSession(ev Event, sm SessionManager, dcs utils.DerivedChargers) *Session {
	s := &Session{cgrid: ev.GetCgrId(),
		uuid:           ev.GetUUID(),
		stopDebit:      make(chan bool),
		sessionManager: sm,
	}
	for _, dc := range dcs {
		if ev.GetReqType(dc.ReqTypeField) != utils.PREPAID {
			continue // We only consider prepaid sessions
		}
		startTime, err := ev.GetAnswerTime(dc.AnswerTimeField)
		if err != nil {
			engine.Logger.Err("Error parsing answer event start time, using time.Now!")
			return nil
		}
		cd := &engine.CallDescriptor{
			Direction:   ev.GetDirection(dc.DirectionField),
			Tenant:      ev.GetTenant(dc.TenantField),
			Category:    ev.GetCategory(dc.CategoryField),
			Subject:     ev.GetSubject(dc.SubjectField),
			Account:     ev.GetAccount(dc.AccountField),
			Destination: ev.GetDestination(dc.DestinationField),
			TimeStart:   startTime}
		sr := &SessionRun{
			runId:          dc.RunId,
			callDescriptor: cd,
		}
		s.sessionRuns = append(s.sessionRuns, sr)
		go s.debitLoop(len(s.sessionRuns) - 1) // Send index of the just appended sessionRun
	}
	if len(s.sessionRuns) == 0 {
		return nil
	}
	return s
}

// the debit loop method (to be stoped by sending somenthing on stopDebit channel)
func (s *Session) debitLoop(runIdx int) {
	nextCd := *s.sessionRuns[runIdx].callDescriptor
	index := 0.0
	debitPeriod := s.sessionManager.GetDebitPeriod()
	for {
		select {
		case <-s.stopDebit:
			return
		default:
		}
		if index > 0 { // first time use the session start time
			nextCd.TimeStart = nextCd.TimeEnd
		}
		nextCd.TimeEnd = nextCd.TimeStart.Add(debitPeriod)
		nextCd.LoopIndex = index
		nextCd.DurationIndex += debitPeriod // first presumed duration
		cc := new(engine.CallCost)
		if err := s.sessionManager.MaxDebit(&nextCd, cc); err != nil {
			engine.Logger.Err(fmt.Sprintf("Could not complete debit opperation: %v", err))
			s.sessionManager.DisconnectSession(s.uuid, SYSTEM_ERROR, "")
			return
		}
		if cc.GetDuration() == 0 {
			s.sessionManager.DisconnectSession(s.uuid, INSUFFICIENT_FUNDS, nextCd.Destination)
			return
		}
		if cc.GetDuration() <= cfg.FSMinDurLowBalance && len(cfg.FSLowBalanceAnnFile) != 0 {
			if _, err := fsock.FS.SendApiCmd(fmt.Sprintf("uuid_broadcast %s %s aleg\n\n", s.uuid, cfg.FSLowBalanceAnnFile)); err != nil {
				engine.Logger.Err(fmt.Sprintf("<SessionManager> Could not send uuid_broadcast to freeswitch: %s", err.Error()))
			}
		}
		s.sessionRuns[runIdx].callCosts = append(s.sessionRuns[runIdx].callCosts, cc)
		nextCd.TimeEnd = cc.GetEndTime() // set debited timeEnd
		// update call duration with real debited duration
		nextCd.DurationIndex -= debitPeriod
		nextCd.DurationIndex += nextCd.GetDuration()
		time.Sleep(cc.GetDuration())
		index++
	}
}

// Stops the debit loop
func (s *Session) Close(ev Event) {
	// engine.Logger.Debug(fmt.Sprintf("Stopping debit for %s", s.uuid))
	if s == nil {
		return
	}
	close(s.stopDebit) // Close the channel so all the sessionRuns listening will be notified
	if _, err := ev.GetEndTime(); err != nil {
		engine.Logger.Err("Error parsing answer event stop time.")
		for idx := range s.sessionRuns {
			s.sessionRuns[idx].callDescriptor.TimeEnd = s.sessionRuns[idx].callDescriptor.TimeStart.Add(s.sessionRuns[idx].callDescriptor.DurationIndex)
		}
	}
	s.SaveOperations()
}

// Nice print for session
func (s *Session) String() string {
	sDump, _ := json.Marshal(s)
	return string(sDump)
}

// Saves call_costs for each session run
func (s *Session) SaveOperations() {
	if s == nil {
		return
	}
	go func() {
		for _, sr := range s.sessionRuns {
			if len(sr.callCosts) == 0 {
				break // There are no costs to save, ignore the operation
			}
			firstCC := sr.callCosts[0]
			for _, cc := range sr.callCosts[1:] {
				firstCC.Merge(cc)
			}
			if s.sessionManager.GetDbLogger() == nil {
				engine.Logger.Err("<SessionManager> Error: no connection to logger database, cannot save costs")
			}
			s.sessionManager.GetDbLogger().LogCallCost(s.cgrid, engine.SESSION_MANAGER_SOURCE, sr.runId, firstCC)
		}
	}()
}
