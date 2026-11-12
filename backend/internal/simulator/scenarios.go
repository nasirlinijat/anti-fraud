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
	{PatternExpat, (*generator).expat},
	{PatternNewBigSpender, (*generator).newBigSpender},
	{PatternFamilyCard, (*generator).familyCard},
	{PatternSessionShopper, (*generator).sessionShopper},
	{PatternSlowCardTesting, (*generator).slowCardTesting},
	{PatternQuietTakeover, (*generator).quietTakeover},
	{PatternDeviceFarm, (*generator).deviceFarm},
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

// The same expats return through the run, so their purchase history shows
// the other country from the start.
func (g *generator) expat(at time.Time) {
	p := g.expats[g.rng.IntN(len(g.expats))]
	g.add(PatternExpat, g.request(p, at, g.regularAmount()))
}

// newBigSpender has the account age of newAccountSpend and covers its
// amounts.
func (g *generator) newBigSpender(at time.Time) {
	p := g.newScenarioPlayer(at, g.minutes(10, 720))
	g.add(PatternNewBigSpender, g.request(p, at, g.amount(30000, 120000)))
}

// familyCard shares the owner's card, billing region and connection, but not
// the account or the device.
func (g *generator) familyCard(at time.Time) {
	owner := g.players[g.rng.IntN(len(g.players))]
	relative := g.establishedPlayer(30)
	relative.card = owner.card
	relative.region, relative.country, relative.ipAddress = owner.region, owner.country, owner.ipAddress
	g.add(PatternFamilyCard, g.request(relative, at, g.regularAmount()))
}

// sessionShopper has the pacing and amounts of slowCardTesting. One session
// in four moves to a second card of the same player.
func (g *generator) sessionShopper(at time.Time) {
	p := g.establishedPlayer(30)
	count := 2 + g.rng.IntN(3)
	secondCardAt := 0
	if g.rng.Float64() < 0.25 {
		secondCardAt = 1 + g.rng.IntN(count-1)
	}
	for i := range count {
		if i > 0 && i == secondCardAt {
			p.card = g.newCard(p.country)
		}
		g.add(PatternSessionShopper, g.request(p, at, g.amount(199, 1999)))
		at = at.Add(g.minutes(5, 45))
	}
}

// slowCardTesting stays below the velocity rule: 3 to 5 small purchases, 5
// to 45 minutes apart, each on another card from the account's country.
func (g *generator) slowCardTesting(at time.Time) {
	p := g.establishedPlayer(30)
	for i := range 3 + g.rng.IntN(3) {
		if i > 0 {
			p.card = g.newCard(p.country)
		}
		g.add(PatternSlowCardTesting, g.request(p, at, g.amount(199, 1999)))
		at = at.Add(g.minutes(5, 45))
	}
}

// quietTakeover keeps the regular player's account, card and country, and
// changes the phone and the connection.
func (g *generator) quietTakeover(at time.Time) {
	p := g.players[g.rng.IntN(len(g.players))]
	p.deviceType, p.deviceName = "mobile", g.pick(mobileDevices)
	p.deviceID = g.id("device")
	p.ipAddress = g.otherIP(p.ipAddress)
	g.add(PatternQuietTakeover, g.request(p, at, g.amount(3000, 30000)))
}

// deviceFarm is 4 to 6 accounts created hours earlier on one emulator and one
// connection, each with its own card. An account makes up to two small
// purchases and ends with a larger one, so under the default ruleset the large
// purchases reach review and the small ones pass.
func (g *generator) deviceFarm(at time.Time) {
	deviceID, ip, country := g.id("device"), g.ipAddress(), g.pick(countries)
	for range 4 + g.rng.IntN(3) {
		p := g.newScenarioPlayer(at, g.minutes(10, 600))
		p.deviceType, p.deviceName, p.deviceID = "mobile", "Android Emulator", deviceID
		p.country, p.ipAddress = country, ip
		p.card = g.newCard(g.pick(countries))
		for range g.rng.IntN(3) {
			g.add(PatternDeviceFarm, g.request(p, at, g.amount(199, 1999)))
			at = at.Add(g.minutes(2, 8))
		}
		g.add(PatternDeviceFarm, g.request(p, at, g.amount(50000, 70000)))
		at = at.Add(g.minutes(2, 8))
	}
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
