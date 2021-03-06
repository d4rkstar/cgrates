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

package engine

import (
	"errors"
	"fmt"
	"log/syslog"
	"strings"
	"time"
	//"encoding/json"
	"github.com/cgrates/cgrates/cache2go"
	"github.com/cgrates/cgrates/history"
	"github.com/cgrates/cgrates/utils"
)

const (
	// these might be better in the confs under optimizations section
	RECURSION_MAX_DEPTH = 3
	MIN_PREFIX_MATCH    = 1
	FALLBACK_SUBJECT    = utils.ANY
	DEBUG               = true
)

func init() {
	var err error
	Logger, err = syslog.New(syslog.LOG_INFO, "CGRateSxb")
	if err != nil {
		Logger = new(utils.StdLogger)
		Logger.Err(fmt.Sprintf("Could not connect to syslog: %v", err))
	}
	if DEBUG {
		dataStorage, _ = NewMapStorage()
		accountingStorage, _ = NewMapStorage()
	} else {
		//dataStorage, _ = NewMongoStorage(db_server, "27017", "cgrates_test", "", "")
		dataStorage, _ = NewRedisStorage("127.0.0.1:6379", 12, "", utils.MSGPACK)
		accountingStorage, _ = NewRedisStorage("127.0.0.1:6379", 13, "", utils.MSGPACK)
	}
	storageLogger = dataStorage.(LogStorage)
}

var (
	Logger                 utils.LoggerInterface
	dataStorage            RatingStorage
	accountingStorage      AccountingStorage
	storageLogger          LogStorage
	debitPeriod            = 10 * time.Second
	globalRoundingDecimals = 10
	historyScribe          history.Scribe
	//historyScribe, _ = history.NewMockScribe()
)

// Exported method to set the storage getter.
func SetRatingStorage(sg RatingStorage) {
	dataStorage = sg
}

func SetAccountingStorage(ag AccountingStorage) {
	accountingStorage = ag
}

// Sets the global rounding method and decimal precision for GetCost method
func SetRoundingDecimals(rd int) {
	globalRoundingDecimals = rd
}

/*
Sets the database for logging (can be de same  as storage getter or different db)
*/
func SetStorageLogger(sg LogStorage) {
	storageLogger = sg
}

/*
Exported method to set the debit period for caching purposes.
*/
func SetDebitPeriod(d time.Duration) {
	debitPeriod = d
}

// Exported method to set the history scribe.
func SetHistoryScribe(scribe history.Scribe) {
	historyScribe = scribe
}

/*
The input stucture that contains call information.
*/
type CallDescriptor struct {
	Direction                             string
	Category                              string
	Tenant, Subject, Account, Destination string
	TimeStart, TimeEnd                    time.Time
	LoopIndex                             float64       // indicates the position of this segment in a cost request loop
	DurationIndex                         time.Duration // the call duration so far (till TimeEnd)
	FallbackSubject                       string        // the subject to check for destination if not found on primary subject
	RatingInfos                           RatingInfos
	Increments                            Increments
	TOR                                   string // used unit balances selector
	// session limits
	MaxRate      float64
	MaxRateUnit  time.Duration
	MaxCost      float64
	MaxCostSoFar float64
	account      *Account
}

func (cd *CallDescriptor) ValidateCallData() error {
	if cd.TimeStart.After(cd.TimeEnd) || cd.TimeStart.Equal(cd.TimeEnd) {
		return errors.New("TimeStart must be strctly before TimeEnd")
	}
	if cd.TimeEnd.Sub(cd.TimeStart) < cd.DurationIndex {
		return errors.New("DurationIndex must be equal or greater than TimeEnd - TimeStart")
	}
	return nil
}

// Adds a rating plan that applyes to current call descriptor.
func (cd *CallDescriptor) AddRatingInfo(ris ...*RatingInfo) {
	cd.RatingInfos = append(cd.RatingInfos, ris...)
}

// Gets and caches the user balance information.
func (cd *CallDescriptor) getAccount() (ub *Account, err error) {
	if cd.account == nil {
		cd.account, err = accountingStorage.GetAccount(cd.GetAccountKey())
	}
	if cd.account != nil && cd.account.Disabled {
		return nil, fmt.Errorf("User %s is disabled", cd.account.Id)
	}
	return cd.account, err
}

/*
Restores the activation periods for the specified prefix from storage.
*/
func (cd *CallDescriptor) LoadRatingPlans() (err error) {
	err = cd.getRatingPlansForPrefix(cd.GetKey(cd.Subject), 1)
	if err != nil || !cd.continousRatingInfos() {
		// use the default subject
		err = cd.getRatingPlansForPrefix(cd.GetKey(FALLBACK_SUBJECT), 1)
	}
	//load the rating plans
	if err != nil || !cd.continousRatingInfos() {
		err = errors.New("Could not determine rating plans for call")
		return
	}
	return
}

// FIXME: this method is not exhaustive but will cover 99% of cases just good
// it will not cover very long calls with very short activation periods for rates
func (cd *CallDescriptor) getRatingPlansForPrefix(key string, recursionDepth int) (err error) {
	if recursionDepth > RECURSION_MAX_DEPTH {
		err = errors.New("Max fallback recursion depth reached!" + key)
		return
	}
	rpf, err := dataStorage.GetRatingProfile(key, false)
	if err != nil || rpf == nil {
		return err
	}
	if err = rpf.GetRatingPlansForPrefix(cd); err != nil || !cd.continousRatingInfos() {
		// try rating profile fallback
		recursionDepth++
		for index := 0; index < len(cd.RatingInfos); index++ {
			ri := cd.RatingInfos[index]
			if len(ri.RateIntervals) > 0 {
				// go to next rate info
				continue
			}
			if len(ri.FallbackKeys) > 0 {
				tempCD := &CallDescriptor{
					Category:    cd.Category,
					Direction:   cd.Direction,
					Tenant:      cd.Tenant,
					Destination: cd.Destination,
				}
				if index == 0 {
					tempCD.TimeStart = cd.TimeStart
				} else {
					tempCD.TimeStart = ri.ActivationTime
				}
				if index == len(cd.RatingInfos)-1 {
					tempCD.TimeEnd = cd.TimeEnd
				} else {
					tempCD.TimeEnd = cd.RatingInfos[index+1].ActivationTime
				}
				for _, fbk := range ri.FallbackKeys {
					if err := tempCD.getRatingPlansForPrefix(fbk, recursionDepth); err != nil {
						continue
					}
					// extract the rate infos and break
					for newIndex, newRI := range tempCD.RatingInfos {
						// check if the new ri is filled
						if len(newRI.RateIntervals) == 0 {
							continue
						}
						if newIndex == 0 {
							cd.RatingInfos[index] = newRI
						} else {
							// insert extra data
							i := index + newIndex
							cd.RatingInfos = append(cd.RatingInfos, nil)
							copy(cd.RatingInfos[i+1:], cd.RatingInfos[i:])
							cd.RatingInfos[i] = newRI
						}
					}
					// if this fallbackey covered the interval than skip
					// the other fallback keys
					if tempCD.continousRatingInfos() {
						break
					}
				}
			}
		}
	}
	return
}

// checks if there is rating info for the entire call duration
func (cd *CallDescriptor) continousRatingInfos() bool {
	if len(cd.RatingInfos) == 0 || cd.RatingInfos[0].ActivationTime.After(cd.TimeStart) {
		return false
	}
	for _, ri := range cd.RatingInfos {
		if ri.RateIntervals == nil {
			return false
		}
	}
	return true
}

// adds a rating infos only if that call period is not already covered
// returns true if added
func (cd *CallDescriptor) addRatingInfos(ris RatingInfos) bool {
	if len(cd.RatingInfos) == 0 {
		cd.RatingInfos = append(cd.RatingInfos, ris...)
		return true
	}
	cd.RatingInfos.Sort()
	// check if we dont have the start covered
	if cd.RatingInfos[0].ActivationTime.After(cd.TimeStart) {
		if ris[0].ActivationTime.Before(cd.RatingInfos[0].ActivationTime) {
			cd.RatingInfos = append(cd.RatingInfos, ris[0])
			cd.RatingInfos.Sort()
		}
	}
	for _, ri := range cd.RatingInfos {
		if ri.RateIntervals == nil {
			for i, new_ri := range ris {
				_ = i
				_ = new_ri
			}
		}
	}
	return true
}

// Constructs the key for the storage lookup.
// The prefixLen is limiting the length of the destination prefix.
func (cd *CallDescriptor) GetKey(subject string) string {
	// check if subject is alias
	if rs, err := cache2go.GetCached(RP_ALIAS_PREFIX + utils.RatingSubjectAliasKey(cd.Tenant, subject)); err == nil {
		realSubject := rs.(string)
		subject = realSubject
		cd.Subject = realSubject
	}
	return fmt.Sprintf("%s:%s:%s:%s", cd.Direction, cd.Tenant, cd.Category, subject)
}

// Returns the key used to retrive the user balance involved in this call
func (cd *CallDescriptor) GetAccountKey() string {
	subj := cd.Subject
	if cd.Account != "" {
		// check if subject is alias
		if realSubject, err := cache2go.GetCached(ACC_ALIAS_PREFIX + utils.AccountAliasKey(cd.Tenant, subj)); err == nil {
			cd.Account = realSubject.(string)
		}
		subj = cd.Account
	}
	return fmt.Sprintf("%s:%s:%s", cd.Direction, cd.Tenant, subj)
}

func (cd *CallDescriptor) GetLCRKey(subj string) string {
	if subj == "" {
		subj = cd.Subject
		if cd.Account != "" {
			subj = cd.Account
		}
	}
	return fmt.Sprintf("%s:%s:%s", cd.Direction, cd.Tenant, subj)
}

// Splits the received timespan into sub time spans according to the activation periods intervals.
func (cd *CallDescriptor) splitInTimeSpans() (timespans []*TimeSpan) {
	firstSpan := &TimeSpan{TimeStart: cd.TimeStart, TimeEnd: cd.TimeEnd, DurationIndex: cd.DurationIndex}

	timespans = append(timespans, firstSpan)
	if len(cd.RatingInfos) == 0 {
		return
	}

	firstSpan.ratingInfo = cd.RatingInfos[0]
	if cd.TOR == MINUTES {
		// split on rating plans
		afterStart, afterEnd := false, false //optimization for multiple activation periods
		for _, rp := range cd.RatingInfos {
			if !afterStart && !afterEnd && rp.ActivationTime.Before(cd.TimeStart) {
				firstSpan.ratingInfo = rp
				firstSpan.MatchedSubject = rp.MatchedSubject
				firstSpan.MatchedPrefix = rp.MatchedPrefix
				firstSpan.MatchedDestId = rp.MatchedDestId
			} else {
				afterStart = true
				for i := 0; i < len(timespans); i++ {
					newTs := timespans[i].SplitByRatingPlan(rp)
					if newTs != nil {
						timespans = append(timespans, newTs)
					} else {
						afterEnd = true
						break
					}
				}
			}
		}

	}
	// Logger.Debug(fmt.Sprintf("After SplitByRatingPlan: %+v", timespans))
	// split on price intervals
	for i := 0; i < len(timespans); i++ {
		//log.Printf("==============%v==================", i)
		//log.Printf("TS: %+v", timespans[i])
		rp := timespans[i].ratingInfo
		// Logger.Debug(fmt.Sprintf("rp: %+v", rp))
		//timespans[i].RatingPlan = nil
		rp.RateIntervals.Sort()
		for _, interval := range rp.RateIntervals {
			//log.Printf("\tINTERVAL: %+v %v", interval, len(rp.RateIntervals))
			if timespans[i].RateInterval != nil && timespans[i].RateInterval.Weight < interval.Weight {
				continue // if the timespan has an interval than it already has a heigher weight
			}
			newTs := timespans[i].SplitByRateInterval(interval, cd.TOR != MINUTES)
			if newTs != nil {
				newTs.ratingInfo = rp
				// insert the new timespan
				index := i + 1
				timespans = append(timespans, nil)
				copy(timespans[index+1:], timespans[index:])
				timespans[index] = newTs
				break
			}
		}
	}

	//Logger.Debug(fmt.Sprintf("After SplitByRateInterval: %+v", timespans))
	//log.Printf("After SplitByRateInterval: %+v", timespans)
	timespans = cd.roundTimeSpansToIncrement(timespans)
	// Logger.Debug(fmt.Sprintf("After round: %+v", timespans))
	//log.Printf("After round: %+v", timespans)
	return
}

// if the rate interval for any timespan has a RatingIncrement larger than the timespan duration
// the timespan must expand potentially overlaping folowing timespans and may exceed call
// descriptor's initial duration
func (cd *CallDescriptor) roundTimeSpansToIncrement(timespans TimeSpans) []*TimeSpan {
	for i := 0; i < len(timespans); i++ {
		ts := timespans[i]
		if ts.RateInterval != nil {
			_, rateIncrement, _ := ts.RateInterval.GetRateParameters(ts.GetGroupStart())
			// if the timespan duration is larger than the rate increment make sure it is a multiple of it
			if rateIncrement < ts.GetDuration() {
				rateIncrement = utils.RoundTo(rateIncrement, ts.GetDuration())
			}
			if rateIncrement > ts.GetDuration() {
				initialDuration := ts.GetDuration()
				ts.TimeEnd = ts.TimeStart.Add(rateIncrement)
				ts.DurationIndex = ts.DurationIndex + (rateIncrement - initialDuration)
				timespans.RemoveOverlapedFromIndex(i)
			}
		}
	}

	return timespans
}

// Returns call descripor's total duration
func (cd *CallDescriptor) GetDuration() time.Duration {
	return cd.TimeEnd.Sub(cd.TimeStart)
}

/*
Creates a CallCost structure with the cost information calculated for the received CallDescriptor.
*/
func (cd *CallDescriptor) GetCost() (*CallCost, error) {
	if cd.DurationIndex < cd.TimeEnd.Sub(cd.TimeStart) {
		cd.DurationIndex = cd.TimeEnd.Sub(cd.TimeStart)
	}
	if cd.TOR == "" {
		cd.TOR = MINUTES
	}
	err := cd.LoadRatingPlans()
	if err != nil {
		Logger.Err(fmt.Sprintf("error getting cost for key %s: %v", cd.GetKey(cd.Subject), err))
		return &CallCost{Cost: -1}, err
	}

	timespans := cd.splitInTimeSpans()
	cost := 0.0

	for i, ts := range timespans {
		// only add connect fee if this is the first/only call cost request
		if cd.LoopIndex == 0 && i == 0 && ts.RateInterval != nil {
			cost += ts.RateInterval.Rating.ConnectFee
		}
		cost += ts.getCost()
	}
	//startIndex := len(fmt.Sprintf("%s:%s:%s:", cd.Direction, cd.Tenant, cd.Category))
	cc := &CallCost{
		Direction:        cd.Direction,
		Category:         cd.Category,
		Tenant:           cd.Tenant,
		Account:          cd.Account,
		Destination:      cd.Destination,
		Subject:          cd.Subject,
		Cost:             cost,
		Timespans:        timespans,
		deductConnectFee: cd.LoopIndex == 0,
		TOR:              cd.TOR,
	}
	// global rounding
	roundingDecimals, roundingMethod := cc.GetLongestRounding()
	cc.Cost = utils.Round(cc.Cost, roundingDecimals, roundingMethod)
	//Logger.Info(fmt.Sprintf("<Rater> Get Cost: %s => %v", cd.GetKey(), cc))
	cc.Timespans.Compress()
	return cc, err
}

/*
Returns the approximate max allowed session for user balance. It will try the max amount received in the call descriptor
If the user has no credit then it will return 0.
If the user has postpayed plan it returns -1.
*/
func (origCD *CallDescriptor) getMaxSessionDuration(account *Account) (time.Duration, error) {
	if origCD.DurationIndex < origCD.TimeEnd.Sub(origCD.TimeStart) {
		origCD.DurationIndex = origCD.TimeEnd.Sub(origCD.TimeStart)
	}
	if origCD.TOR == "" {
		origCD.TOR = MINUTES
	}
	cd := origCD.Clone()
	//Logger.Debug(fmt.Sprintf("MAX SESSION cd: %+v", cd))
	err := cd.LoadRatingPlans()
	if err != nil {
		Logger.Err(fmt.Sprintf("error getting cost for key %v: %v", cd.GetAccountKey(), err))
		return 0, err
	}
	var availableDuration time.Duration
	availableCredit := 0.0
	if account.AllowNegative {
		return -1, nil
	} else {
		availableDuration, availableCredit, _ = account.getCreditForPrefix(cd)
		// Logger.Debug(fmt.Sprintf("available sec: %v credit: %v", availableSeconds, availableCredit))
	}
	if cd.MaxCost > 0 {
		// limit availableCredit
		if cd.MaxCostSoFar+availableCredit > cd.MaxCost {
			availableCredit = cd.MaxCost - cd.MaxCostSoFar
		}
	}
	//Logger.Debug(fmt.Sprintf("availableDuration: %v, availableCredit: %v", availableDuration, availableCredit))
	initialDuration := cd.TimeEnd.Sub(cd.TimeStart)
	if initialDuration <= availableDuration {
		// there are enough minutes for requested interval
		return initialDuration, nil
	}
	//Logger.Debug(fmt.Sprintf("initial Duration: %v", initialDuration))
	// we must move the timestart for the interval with the available duration because
	// that was already checked
	cd.TimeStart = cd.TimeStart.Add(availableDuration)

	// substract the connect fee
	cc, err := cd.GetCost()
	if availableDuration == 0 && cc.deductConnectFee { // only if we did not already used minutes
		availableCredit -= cc.GetConnectFee()
	}
	// check for zero balance
	if (availableCredit < 0) || (availableCredit == 0 && cc.Cost > 0) {
		return utils.MinDuration(initialDuration, availableDuration), nil
	}
	if err != nil {
		Logger.Err(fmt.Sprintf("Could not get cost for %s: %s.", cd.GetKey(cd.Subject), err.Error()))
		return 0, err
	}
	// now let's check how many increments are covered with the avilableCredit
	// also check for max rate/max rate unit
	for _, ts := range cc.Timespans {
		ts.createIncrementsSlice()
		//Logger.Debug(fmt.Sprintf("TS: %+v", ts))
		if cd.MaxRate > 0 && cd.MaxRateUnit > 0 {
			rate, _, rateUnit := ts.RateInterval.GetRateParameters(ts.GetGroupStart())
			if rate/rateUnit.Seconds() > cd.MaxRate/cd.MaxRateUnit.Seconds() {
				return availableDuration, nil
			}
		}
		for _, incr := range ts.Increments {
			if incr.Cost <= availableCredit {
				availableCredit -= incr.Cost
				availableDuration += incr.Duration
			} else {
				return availableDuration, nil
			}
		}
	}
	if initialDuration < availableDuration {
		return initialDuration, nil
	}
	return utils.MinDuration(initialDuration, availableDuration), nil
}

func (cd *CallDescriptor) GetMaxSessionDuration() (duration time.Duration, err error) {
	if account, err := cd.getAccount(); err != nil || account == nil {
		Logger.Err(fmt.Sprintf("Could not get user balance for %s: %s.", cd.GetAccountKey(), err.Error()))
		return 0, err
	} else {
		if memberIds, err := account.GetUniqueSharedGroupMembers(cd.Destination, cd.Direction, cd.Category, cd.TOR); err == nil {
			AccLock.GuardMany(memberIds, func() (float64, error) {
				duration, err = cd.getMaxSessionDuration(account)
				return 0, err
			})
		} else {
			return 0, err
		}
		return duration, err
	}
}

// Interface method used to add/substract an amount of cents or bonus seconds (as returned by GetCost method)
// from user's money balance.
func (cd *CallDescriptor) debit(account *Account) (cc *CallCost, err error) {
	cc, err = cd.GetCost()
	cc.Timespans.Decompress()
	if err != nil {
		Logger.Err(fmt.Sprintf("<Rater> Error getting cost for account key %v: %v", cd.GetAccountKey(), err))
		return
	}
	//Logger.Debug(fmt.Sprintf("<Rater> Attempting to debit from %v, value: %v", cd.GetAccountKey(), cc.Cost+cc.ConnectFee))
	defer accountingStorage.SetAccount(account)
	//ub, _ := json.Marshal(account)
	//Logger.Debug(fmt.Sprintf("Account: %s", ub))
	//cCost, _ := json.Marshal(cc)
	//Logger.Debug(fmt.Sprintf("CallCost: %s", cCost))
	if cc.Cost != 0 || (cc.deductConnectFee && cc.GetConnectFee() != 0) {
		account.debitCreditBalance(cc, true)
	}
	cost := 0.0
	// re-calculate call cost after balances
	if cc.deductConnectFee { // add back the connectFee
		cost += cc.GetConnectFee()
	}
	for _, ts := range cc.Timespans {
		cost += ts.getCost()
		cost = utils.Round(cost, globalRoundingDecimals, utils.ROUNDING_MIDDLE) // just get rid of the extra decimals
	}
	cc.Cost = cost
	cc.Timespans.Compress()
	return
}

func (cd *CallDescriptor) Debit() (cc *CallCost, err error) {
	// lock all group members
	if account, err := cd.getAccount(); err != nil || account == nil {
		Logger.Err(fmt.Sprintf("Could not get user balance for %s: %s.", cd.GetAccountKey(), err.Error()))
		return nil, err
	} else {
		if memberIds, err := account.GetUniqueSharedGroupMembers(cd.Destination, cd.Direction, cd.Category, cd.TOR); err == nil {
			AccLock.GuardMany(memberIds, func() (float64, error) {
				cc, err = cd.debit(account)
				return 0, err
			})
		} else {
			return nil, err
		}
		return cc, err
	}
}

// Interface method used to add/substract an amount of cents or bonus seconds (as returned by GetCost method)
// from user's money balance.
// This methods combines the Debit and GetMaxSessionDuration and will debit the max available time as returned
// by the GetMaxSessionTime method. The amount filed has to be filled in call descriptor.
func (cd *CallDescriptor) MaxDebit() (cc *CallCost, err error) {
	if account, err := cd.getAccount(); err != nil || account == nil {
		Logger.Err(fmt.Sprintf("Could not get user balance for %s: %s.", cd.GetAccountKey(), err.Error()))
		return nil, err
	} else {
		if memberIds, err := account.GetUniqueSharedGroupMembers(cd.Destination, cd.Direction, cd.Category, cd.TOR); err == nil {
			AccLock.GuardMany(memberIds, func() (float64, error) {
				remainingDuration, err := cd.getMaxSessionDuration(account)
				if err != nil || remainingDuration == 0 {
					cc, err = new(CallCost), fmt.Errorf("no more credit: %v", err)
					return 0, err
				}
				if remainingDuration > 0 { // for postpaying client returns -1
					cd.TimeEnd = cd.TimeStart.Add(remainingDuration)
				}
				cc, err = cd.debit(account)
				//log.Print(balanceMap[0].Value, balanceMap[1].Value)
				return 0, err
			})
		} else {
			return nil, err
		}
	}
	return cc, err
}

func (cd *CallDescriptor) RefundIncrements() (left float64, err error) {
	accountsCache := make(map[string]*Account)
	for _, increment := range cd.Increments {
		account, found := accountsCache[increment.BalanceInfo.AccountId]
		if !found {
			if acc, err := accountingStorage.GetAccount(increment.BalanceInfo.AccountId); err == nil && acc != nil {
				account = acc
				accountsCache[increment.BalanceInfo.AccountId] = account
				defer accountingStorage.SetAccount(account)
			}
		}
		account.refundIncrement(increment, cd.Direction, cd.TOR, true)
	}
	return 0.0, err
}

func (cd *CallDescriptor) FlushCache() (err error) {
	cache2go.Flush()
	dataStorage.CacheRating(nil, nil, nil, nil, nil)
	accountingStorage.CacheAccounting(nil, nil, nil, nil)
	return nil

}

// Creates a CallCost structure copying related data from CallDescriptor
func (cd *CallDescriptor) CreateCallCost() *CallCost {
	return &CallCost{
		Direction:   cd.Direction,
		Category:    cd.Category,
		Tenant:      cd.Tenant,
		Subject:     cd.Subject,
		Account:     cd.Account,
		Destination: cd.Destination,
		TOR:         cd.TOR,
	}
}

func (cd *CallDescriptor) Clone() *CallDescriptor {
	return &CallDescriptor{
		Direction:       cd.Direction,
		Category:        cd.Category,
		Tenant:          cd.Tenant,
		Subject:         cd.Subject,
		Account:         cd.Account,
		Destination:     cd.Destination,
		TimeStart:       cd.TimeStart,
		TimeEnd:         cd.TimeEnd,
		LoopIndex:       cd.LoopIndex,
		DurationIndex:   cd.DurationIndex,
		MaxRate:         cd.MaxRate,
		MaxRateUnit:     cd.MaxRateUnit,
		MaxCost:         cd.MaxCost,
		MaxCostSoFar:    cd.MaxCostSoFar,
		FallbackSubject: cd.FallbackSubject,
		//RatingInfos:     cd.RatingInfos,
		//Increments:      cd.Increments,
		TOR: cd.TOR,
	}
}

func (cd *CallDescriptor) GetLCR() (*LCRCost, error) {
	lcr, err := dataStorage.GetLCR(cd.GetLCRKey(""), false)
	if err != nil || lcr == nil {
		// try the *any customer
		if lcr, err = dataStorage.GetLCR(cd.GetLCRKey(utils.ANY), false); err != nil || lcr == nil {
			return nil, err
		}
	}
	lcr.Sort()
	lcrCost := &LCRCost{
		TimeSpans: []*LCRTimeSpan{&LCRTimeSpan{StartTime: cd.TimeStart}},
	}
	for _, lcrActivation := range lcr.Activations {
		// TODO: filer entry by destination
		lcrEntry := lcrActivation.GetLCREntryForPrefix(cd.Destination)
		if lcrActivation.ActivationTime.Before(cd.TimeStart) ||
			lcrActivation.ActivationTime.Equal(cd.TimeStart) {
			lcrCost.TimeSpans[0].Entry = lcrEntry
		} else {
			if lcrActivation.ActivationTime.Before(cd.TimeEnd) {
				// add lcr timespan
				lcrCost.TimeSpans = append(lcrCost.TimeSpans, &LCRTimeSpan{
					StartTime: lcrActivation.ActivationTime,
					Entry:     lcrEntry,
				})
			}
		}
	}
	for _, ts := range lcrCost.TimeSpans {
		if ts.Entry.Strategy == LCR_STRATEGY_STATIC {
			for _, supplier := range strings.Split(ts.Entry.Suppliers, ";") {
				supplier = strings.TrimSpace(supplier)
				lcrCD := cd.Clone()
				lcrCD.Subject = supplier
				if cc, err := lcrCD.GetCost(); err != nil || cc == nil {
					ts.SupplierCosts = append(ts.SupplierCosts, &LCRSupplierCost{
						Supplier: supplier,
						Error:    err,
					})
				} else {
					ts.SupplierCosts = append(ts.SupplierCosts, &LCRSupplierCost{
						Supplier: supplier,
						Cost:     cc.Cost,
					})
				}
			}
		} else {
			// find rating profiles
			ratingProfileSearchKey := fmt.Sprintf("%s:%s:%s:", lcr.Direction, lcr.Tenant, ts.Entry.Category)
			suppliers := cache2go.GetEntriesKeys(LCR_PREFIX + ratingProfileSearchKey)
			for _, supplier := range suppliers {
				split := strings.Split(supplier, ":")
				supplier = split[len(split)-1]
				lcrCD := cd.Clone()
				lcrCD.Subject = supplier
				if cc, err := lcrCD.GetCost(); err != nil || cc == nil {
					ts.SupplierCosts = append(ts.SupplierCosts, &LCRSupplierCost{
						Supplier: supplier,
						Error:    err,
					})
				} else {
					ts.SupplierCosts = append(ts.SupplierCosts, &LCRSupplierCost{
						Supplier: supplier,
						Cost:     cc.Cost,
					})
				}
			}
			// sort according to strategy
			ts.Sort()
		}
	}
	return lcrCost, nil
}
