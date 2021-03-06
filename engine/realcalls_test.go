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
	"encoding/json"
	"testing"
)

var balance1 string = `{"Id":"*out:192.168.56.66:dan","Type":"*prepaid","BalanceMap":{"*monetary*out":[{"Uuid":"7fe5d6e740b6edd180b96274b8bd4123","Value":10,"ExpirationDate":"0001-01-01T00:00:00Z","Weight":10,"GroupIds":null,"DestinationId":"*any","RateSubject":""}]},"UnitCounters":null,"ActionTriggers":[{"Id":"120ea04d40af91c580adb0da11554c88","BalanceId":"*monetary","Direction":"*out","ThresholdType":"*min_balance","ThresholdValue":2,"DestinationId":"","Weight":10,"ActionsId":"LOG_BALANCE","Executed":false},{"Id":"fa217a904059cfd3806239f5ad229f4a","BalanceId":"*monetary","Direction":"*out","ThresholdType":"*max_balance","ThresholdValue":20,"DestinationId":"","Weight":10,"ActionsId":"LOG_BALANCE","Executed":false},{"Id":"f05174b740ab987c802a0a29aa5a2764","BalanceId":"*monetary","Direction":"*out","ThresholdType":"*max_counter","ThresholdValue":15,"DestinationId":"FS_USERS","Weight":10,"ActionsId":"LOG_BALANCE","Executed":false},{"Id":"4d6ebf454048371280100094246163a7","BalanceId":"*monetary","Direction":"*out","ThresholdType":"*min_balance","ThresholdValue":0.1,"DestinationId":"","Weight":10,"ActionsId":"WARN_HTTP","Executed":false}],"Groups":null,"UserIds":null}`

var callCost1 string = `{"Direction":"*out","TOR":"call","Tenant":"192.168.56.66","Subject":"dan","Account":"dan","Destination":"+4986517174963","Cost":0.6,"ConnectFee":0,"Timespans":[{"TimeStart":"2013-12-03T14:36:48+01:00","TimeEnd":"2013-12-03T14:37:48+01:00","Cost":0.6,"RateInterval":{"Timing":{"Years":[],"Months":[],"MonthDays":[],"WeekDays":[],"StartTime":"00:00:00","EndTime":""},"Rating":{"ConnectFee":0,"Rates":[{"GroupIntervalStart":0,"Value":0.6,"RateIncrement":60000000000,"RateUnit":60000000000}],"RoundingMethod":"*up","RoundingDecimals":2},"Weight":10},"DurationIndex":60000000000,"Increments":null,"MatchedSubject":"*out:192.168.56.66:call:*any","MatchedPrefix":"+49"}]}`

func TestDebitBalanceForCall1(t *testing.T) {
	b1 := new(Account)
	if err := json.Unmarshal([]byte(balance1), b1); err != nil {
		t.Error("Error restoring balance1: ", err)
	}
	cc1 := new(CallCost)
	if err := json.Unmarshal([]byte(callCost1), cc1); err != nil {
		t.Error("Error restoring callCost1: ", err)
	}
	err := b1.debitCreditBalance(cc1, false)
	if err != nil {
		t.Error("Error debiting balance: ", err)
	}
	if b1.BalanceMap[CREDIT+OUTBOUND][0].Value != 9.4 {
		t.Error("Error debiting from balance: ", b1.BalanceMap[CREDIT+OUTBOUND][0])
	}
}

var balanceInsufficient = `{"Id":"*out:192.168.56.66:dan","Type":"*prepaid","BalanceMap":{"*monetary*out":[{"Uuid":"de49cb1a40b2740a8087a1d28112e11c","Value":1,"ExpirationDate":"0001-01-01T00:00:00Z","Weight":10,"GroupIds":null,"DestinationId":"*any","RateSubject":""}]},"UnitCounters":[{"Direction":"*out","BalanceId":"*monetary","Balances":[{"Uuid":"","Value":4,"ExpirationDate":"0001-01-01T00:00:00Z","Weight":0,"GroupIds":null,"DestinationId":"","RateSubject":""}]}],"ActionTriggers":[{"Id":"a33f036e402c2b47801f1380c5c82564","BalanceId":"*monetary","Direction":"*out","ThresholdType":"*min_balance","ThresholdValue":2,"DestinationId":"","Weight":10,"ActionsId":"LOG_BALANCE","Executed":true},{"Id":"91776f2d40496c03806cdbc73ae6b5f9","BalanceId":"*monetary","Direction":"*out","ThresholdType":"*max_balance","ThresholdValue":20,"DestinationId":"","Weight":10,"ActionsId":"LOG_BALANCE","Executed":false},{"Id":"e85e933a4045f77c80ef647295ae209b","BalanceId":"*monetary","Direction":"*out","ThresholdType":"*max_counter","ThresholdValue":15,"DestinationId":"FS_USERS","Weight":10,"ActionsId":"LOG_BALANCE","Executed":false},{"Id":"89c178b440a640b6802334b17b63cd4e","BalanceId":"*monetary","Direction":"*out","ThresholdType":"*min_balance","ThresholdValue":2.1,"DestinationId":"","Weight":10,"ActionsId":"WARN_HTTP","Executed":true}],"Groups":null,"UserIds":null}`

var costInsufficient = `{"Direction":"*out","TOR":"call","Tenant":"192.168.56.66","Subject":"dan","Account":"dan","Destination":"+4986517174963","Cost":1,"ConnectFee":3,"Timespans":[{"TimeStart":"2013-12-05T09:52:17+01:00","TimeEnd":"2013-12-05T09:53:17+01:00","Cost":1,"RateInterval":{"Timing":{"Years":[],"Months":[],"MonthDays":[],"WeekDays":[],"StartTime":"00:00:00","EndTime":""},"Rating":{"ConnectFee":3,"Rates":[{"GroupIntervalStart":0,"Value":1,"RateIncrement":60000000000,"RateUnit":60000000000}],"RoundingMethod":"*up","RoundingDecimals":2},"Weight":10},"DurationIndex":60000000000,"Increments":null,"MatchedSubject":"*out:192.168.56.66:call:*any","MatchedPrefix":"+49"}]}`

func TestDebitInsufficientBalance(t *testing.T) {
	b1 := new(Account)
	if err := json.Unmarshal([]byte(balanceInsufficient), b1); err != nil {
		t.Error("Error restoring balance1: ", err)
	}
	cc1 := new(CallCost)
	if err := json.Unmarshal([]byte(costInsufficient), cc1); err != nil {
		t.Error("Error restoring callCost1: ", err)
	}
	cc1.deductConnectFee = true
	b1.debitCreditBalance(cc1, false)
	/*if err == nil {
		t.Error("Error showing debiting balance error: ", err)
	}*/
	if b1.BalanceMap[CREDIT+OUTBOUND].GetTotalValue() != -3 {
		t.Logf("CC: %+v", cc1.Cost)
		t.Errorf("Error debiting from balance: %+v", b1.BalanceMap[CREDIT+OUTBOUND][0])
	}
}
