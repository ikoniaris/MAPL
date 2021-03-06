// Package MAPL_enginge provides an engine to test messages against policy rules written in MAPL.
package MAPL_engine

import (
	"strings"
	"regexp"
	"fmt"
)

// general action codes
const (
	DEFAULT int = iota
	ALLOW
	ALERT
	BLOCK
)

var ActionTypeNames = [...]string{
	DEFAULT: "rules do not apply to message - block by default",
	ALLOW: "allow",
	ALERT: "alert",
	BLOCK: "block",
}
// Check is the main function to test if any of the rules is applicable for the message and decide according
// to those rules' decisions.

func Check(message *MessageAttributes, rules *Rules) (decision int, descisionString string, relevantRuleIndex int,results []int,appliedRulesIndices []int) {
	//
	// for each message we check its attributes against all of the rules and return a decision
	//

	N := len(rules.Rules)

	results = make([]int, N)
	sem := make(chan int, N) // semaphore pattern
	if true{  // check in parallel

		for i, rule := range (rules.Rules) { // check all the rules in parallel
			go func(in_i int, in_rule Rule) {
				results[in_i] = CheckOneRule(message, &in_rule)
				sem <- 1 // mark that the one rule check is finished
			}(i, rule)

		}

		// wait for all goroutines to finish
		for i := 0; i < N; i++ {
			<-sem
		}

	}else{ // used for debugging

		for in_i,in_rule := range(rules.Rules) {
			results[in_i] = CheckOneRule(message, &in_rule)
		}
	}

	// go over the results and test by order of precedence
	appliedRulesIndices = make([]int, 0)
	relevantRuleIndex = -1

	max_decision := DEFAULT
	for i := 0; i < N; i++ {
		if results[i]>DEFAULT {
			appliedRulesIndices = append(appliedRulesIndices,i)
		}
		if results[i]> max_decision {
			max_decision = results[i]
			relevantRuleIndex = i
		}
	}
	decision = max_decision
	descisionString = ActionTypeNames[decision]
	return decision,descisionString,relevantRuleIndex, results, appliedRulesIndices
}

// CheckOneRules gives the result of testing the message attributes with of one rule
func CheckOneRule(message *MessageAttributes, rule *Rule) int {
	// ----------------------
	// compare basic message attributes:

	match:=TestSender(rule,message)
	if !match{
		return DEFAULT
	}

	match=TestReceiver(rule,message)
	if !match{
		return DEFAULT
	}

	match = rule.OperationRegex.Match([]byte(message.RequestMethod)) // supports wildcards
	if !match{
		return DEFAULT
	}

	// ----------------------
	// compare resource:
	if rule.Protocol != "*"{
		if !strings.EqualFold(message.ContextProtocol, rule.Protocol) { // regardless of case // need to support wildcards!
			return DEFAULT
		}

		if message.ContextType != rule.Resource.ResourceType { // need to support wildcards?
			return DEFAULT
		}

		match = rule.Resource.ResourceNameRegex.Match([]byte(message.RequestPath)) // supports wildcards
		if !match {
			return DEFAULT
		}
	}

	// ----------------------
	// test conditions:
	conditionsResult := true // if there are no conditions then we skip the test and return the rule.Decision
	if len(rule.DNFConditions)>0{
		conditionsResult = TestConditions(rule, message)
	}
	if conditionsResult == false {
		return DEFAULT
	}

	// ----------------------
	// if we got here then the rule applies and we use the rule's decision
	switch rule.Decision{
	case "allow","ALLOW","Allow":
		return ALLOW
	case "alert", "ALERT","Alert":
		return ALERT
	case "block","BLOCK","Block":
			return BLOCK
	}
	return DEFAULT
}

func TestSender(rule *Rule, message *MessageAttributes) bool {
	match := false
	for _, expandedSender := range (rule.Sender.SenderList) {
		match_temp := false

		switch expandedSender.Type {
		case "subnet":
			if expandedSender.IsIP {
				match_temp = (expandedSender.Name == message.SourceIp)
			}
			if expandedSender.IsCIDR {
				match_temp = expandedSender.CIDR.Contains(message.SourceNetIp)
			}
		case "*", "service":
			match_temp = expandedSender.Regexp.Match([]byte(message.SourceService)) // supports wildcards
		default:
			panic("type not supported")
		}
		if match_temp == true {
			match = true
			break
		}
	}
	return match
}

func TestReceiver(rule *Rule, message *MessageAttributes) bool {
	match := false
	for _, expandedReceiver := range (rule.Receiver.ReceiverList) {
		match_temp := false

		switch expandedReceiver.Type {
		case "subnet":
			if expandedReceiver.IsIP {
				match_temp = (expandedReceiver.Name == message.DestinationIp)
			}
			if expandedReceiver.IsCIDR {
				match_temp = expandedReceiver.CIDR.Contains(message.DestinationNetIp)
			}
		case "*", "service":
			match_temp = expandedReceiver.Regexp.Match([]byte(message.DestinationService)) // supports wildcards
		default:
			panic("type not supported")
		}

		if match_temp == true {
			match = true
			break
		}
	}
	return match
}
// testConditions tests the conditions of the rule with the message attributes
func TestConditions(rule *Rule, message *MessageAttributes) bool{
	//
	dnfConditions:=rule.DNFConditions
	res:=make([]bool, len(dnfConditions))
	for i_andCondtions, andConditions:=range(dnfConditions){
		temp_res:=true
		for _, condition:=range(andConditions.ANDConditions){ // calculate AND clauses
			oneConditionResult:=testOneCondition(&condition,message) // test one condition
			temp_res = temp_res && oneConditionResult // logic AND
		}
		res[i_andCondtions] = temp_res
	}

	output := false  // calculate OR of all the AND clauses
	for _, r := range(res){
		output = output || r // logic OR
	}
	return output
}

// testOneCondition tests one condition of the rule with the message attributes
func testOneCondition(c *Condition,message *MessageAttributes) bool {
	// ---------------
	// currently we support the following attributes:
	// payloadSize
	// requestUseragent
	// utcHoursFromMidnight
	// ---------------
	var valueToCompareInt int64
	var valueToCompareFloat float64
	var valueToCompareString string

	result:=false
	// select type of test by types of attribute and methods
	switch (c.Attribute){
	case "true","TRUE":
		result = true
	case "false","FALSE":
		result = false
	case("payloadSize"):
		valueToCompareInt = message.RequestSize
		result = compareIntFunc(valueToCompareInt, c.Method, c.ValueInt)
	case("requestUseragent"):
		valueToCompareString = message.RequestUseragent
		if c.Method == "RE" || c.Method == "re" || c.Method == "NRE" || c.Method == "nre" {
			result = compareRegexFunc(valueToCompareString, c.Method, c.ValueRegex)
		}else{
			result = compareStringWithWildcardsFunc(valueToCompareString, c.Method, c.ValueStringRegex)
		}
	case("utcHoursFromMidnight"):
		valueToCompareFloat = message.RequestTimeHoursFromMidnightUTC
		result = compareFloatFunc(valueToCompareFloat, c.Method, c.ValueFloat)
	case("minuteParity"):
		valueToCompareInt = message.RequestTimeMinutesParity
		result = compareIntFunc(valueToCompareInt, c.Method, c.ValueInt)
		fmt.Println("message.RequestTimeMinutesParity=",message.RequestTimeMinutesParity,valueToCompareInt,c.Method, c.ValueInt)
	case("senderLabel"):
		if c.AttributeIsSenderLabel==false{
			panic("senderLabel without the correct format")
		}
		if valueToCompareString1,ok := message.SourceLabels[c.AttributeSenderLabelKey]; ok { // enter the block only if the key exists
			if c.ValueIsReceiverLabel {
				if valueToCompareString2,ok2 := message.DestinationLabels[c.ValueReceiverLabelKey];ok2 {
					if c.Method == "RE" || c.Method == "re" || c.Method == "NRE" || c.Method == "nre" {
						panic("wrong method with comparison of two labels")
					}
					result = compareStringFunc(valueToCompareString1, c.Method, valueToCompareString2) // string comparison without wildcards
				}
			} else {
				if c.Method == "RE" || c.Method == "re" || c.Method == "NRE" || c.Method == "nre" {
					result = compareRegexFunc(valueToCompareString1, c.Method, c.ValueRegex)
				} else {
					if c.Method == "EX" || c.Method == "ex" { // just test the existence of the key
						result = true
					} else {
						result = compareStringWithWildcardsFunc(valueToCompareString1, c.Method, c.ValueStringRegex) // string comparison with wildcards
					}
				}
			}
		}
	case("receiverLabel"):
		if c.AttributeIsReceiverLabel==false{
			panic("receiverLabel without the correct format")
		}
		if valueToCompareString1,ok := message.DestinationLabels[c.AttributeReceiverLabelKey]; ok { // enter the block only if the key exists
			if c.Method == "RE" || c.Method == "re" || c.Method == "NRE" || c.Method == "nre" {
				result = compareRegexFunc(valueToCompareString1, c.Method, c.ValueRegex)
			} else {
				if c.Method == "EX" || c.Method == "ex" { // just test the existence of the key
					result = true
				} else {
					result = compareStringWithWildcardsFunc(valueToCompareString1, c.Method, c.ValueStringRegex) // compare strings with wildcards
				}
			}
		}

	default:
		panic("condition keyword not supported")
	}
	return result
}

// compareIntFunc compares one int value according the method string.
func compareIntFunc(value1 int64, method string ,value2 int64) bool{ //value2 is the reference value from the rule
	switch(method){
	case "EQ","eq":
		return(value1==value2)
	case "NEQ","neq":
		return(value1!=value2)
	case "LE","le":
		return(value1<=value2)
	case "LT","lt":
		return(value1<value2)
	case "GE","ge":
		return(value1>=value2)
	case "GT","gt":
		return(value1>value2)
	}
	return false
}
// compareFloatFunc compares one float value according the method string.
func compareFloatFunc(value1 float64, method string ,value2 float64) bool{ //value2 is the reference value from the rule
	switch(method){
	case "EQ","eq":
		return(value1==value2)
	case "NEQ","neq":
		return(value1!=value2)
	case "LE","le":
		return(value1<=value2)
	case "LT","lt":
		return(value1<value2)
	case "GE","ge":
		return(value1>=value2)
	case "GT","gt":
		return(value1>value2)
	}
	return false
}
// compareStringFunc compares one string value according the method string
func compareStringFunc(value1 string, method string ,value2 string) bool{
	switch(method){
	case "EQ","eq":
		return(value1==value2)
	case "NEQ","neq":
		return(value1!=value2)
	}
	return false
}
// compareStringWithWildcardsFunc compares one string value according the method string (supports wildcards)
func compareStringWithWildcardsFunc(value1 string, method string ,value2 *regexp.Regexp) bool{
	switch(method){
	case "EQ","eq":
		return (value2.MatchString(value1))
	case "NEQ","neq":
		return !(value2.MatchString(value1))
	}
	return false

}
// compareRegexFunc compares one string value according the regular expression string.
func compareRegexFunc(value1 string, method string ,value2 *regexp.Regexp) bool{ //value2 is the reference value from the rule
	switch(method){
	case "RE","re":
		return (value2.MatchString(value1))
	case "NRE","nre":
		return !(value2.MatchString(value1))
	}
	return false
}

