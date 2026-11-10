package simulator

import "time"

var specialScenarios = [...]struct {
	pattern  Pattern
	generate func(*generator, time.Time)
}{
	{PatternCardTesting, (*generator).cardTesting},
	{PatternAccountTakeover, (*generator).accountTakeover},
	{PatternNewAccountSpend, (*generator).newAccountSpend},
	{PatternTraveler, (*generator).traveler},
	{PatternNewPhone, (*generator).newPhone},
	{PatternInternetCafe, (*generator).internetCafe},
	{PatternBurstBuyer, (*generator).burstBuyer},
	{PatternNewCasualPlayer, (*generator).newCasualPlayer},
	{PatternReturningPlayer, (*generator).returningPlayer},
	{PatternStolenCard, (*generator).stolenCard},
	{PatternLowAndSlow, (*generator).lowAndSlow},
}

func (g *generator) traveler(at time.Time) {
	p := g.players[g.rng.IntN(len(g.players))]
	p.country = g.otherCountry(p.country)
	p.ipAddress = g.otherIP(p.ipAddress)
	g.add(PatternTraveler, g.request(p, at, g.regularAmount()))
}

func (g *generator) newPhone(at time.Time) {
	p := g.players[g.rng.IntN(len(g.players))]
	p.deviceType, p.deviceName = "mobile", g.pick(mobileDevices)
	p.deviceID = g.id("device")
	g.add(PatternNewPhone, g.request(p, at, g.regularAmount()))
}

func (g *generator) internetCafe(at time.Time) {
	deviceID, ip := g.id("device"), g.ipAddress()
	country := g.pick(countries)
	for range 2 + g.rng.IntN(2) {
		p := g.establishedPlayer(30)
		p.deviceType, p.deviceName, p.deviceID = "desktop", "Windows", deviceID
		p.country, p.card.country, p.ipAddress = country, country, ip
		g.add(PatternInternetCafe, g.request(p, at, g.ordinaryAmount()))
		at = at.Add(g.seconds(15, 90))
	}
}

func (g *generator) burstBuyer(at time.Time) {
	p := g.establishedPlayer(30)
	for range 4 + g.rng.IntN(4) {
		g.add(PatternBurstBuyer, g.request(p, at, g.amount(199, 1999)))
		at = at.Add(g.seconds(10, 45))
	}
}

func (g *generator) newCasualPlayer(at time.Time) {
	p := g.newScenarioPlayer(at, g.minutes(30, 360))
	g.add(PatternNewCasualPlayer, g.request(p, at, g.amount(199, 1999)))
}

// The returning player's earlier activity predates the simulated traffic.
func (g *generator) returningPlayer(at time.Time) {
	p := g.establishedPlayer(180)
	g.add(PatternReturningPlayer, g.request(p, at, g.ordinaryAmount()))
}

func (g *generator) stolenCard(at time.Time) {
	owner := g.players[g.rng.IntN(len(g.players))]
	buyer := g.establishedPlayer(30)
	buyer.card = owner.card
	if buyer.ipAddress == owner.ipAddress {
		buyer.ipAddress = g.otherIP(owner.ipAddress)
	}
	g.add(PatternStolenCard, g.request(buyer, at, g.amount(1499, 24999)))
}

func (g *generator) lowAndSlow(at time.Time) {
	p := g.establishedPlayer(365)
	g.add(PatternLowAndSlow, g.request(p, at, g.ordinaryAmount()))
}

func (g *generator) establishedPlayer(minDays int) player {
	return g.newPlayer(g.cfg.Start.Add(-g.days(minDays, 900)))
}

func (g *generator) otherIP(previous string) string {
	for {
		if ip := g.ipAddress(); ip != previous {
			return ip
		}
	}
}
