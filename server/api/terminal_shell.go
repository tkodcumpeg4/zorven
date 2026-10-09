package api

// issueTerminalWithShell, terminal bileti uretir; secilen kabuk ID'sini, yeniden
// baglanilacak oturumu ve bileti alan kimligi bilete baglar. Ajan kabuk ID'sini
// yine kendi listesine karsi dogrular.
func (ts *TicketStore) issueTerminalWithShell(tenantID, clientID, shell, attach, owner string) string {
	tok := ts.issue(ticketKindTerminal, tenantID, clientID)
	ts.mu.Lock()
	if info, ok := ts.tickets[tok]; ok {
		info.Shell, info.Attach, info.Owner = shell, attach, owner
		ts.tickets[tok] = info
	}
	ts.mu.Unlock()
	return tok
}

// peekTerminal, bileti TUKETMEDEN terminal bilgilerini okur (redeem'den once cagrilir).
func (ts *TicketStore) peekTerminal(tok string) TicketInfo {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.tickets[tok]
}
