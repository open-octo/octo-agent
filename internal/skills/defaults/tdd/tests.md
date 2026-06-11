# Good and Bad Tests

## Good Tests

**Integration-style**: Test through real interfaces, not mocks of internal parts.

```typescript
// GOOD: Tests observable behavior
test("user can checkout with valid cart", async () => {
  const cart = createCart();
  cart.add(product);
  const result = await checkout(cart, paymentMethod);
  expect(result.status).toBe("confirmed");
});
```

Characteristics:

- Tests behavior users/callers care about
- Uses public API only
- Survives internal refactors
- Describes WHAT, not HOW
- One logical assertion per test

## Bad Tests

**Implementation-detail tests**: Coupled to internal structure.

```typescript
// BAD: Tests implementation details
test("checkout calls paymentService.process", async () => {
  const mockPayment = jest.mock(paymentService);
  await checkout(cart, payment);
  expect(mockPayment.process).toHaveBeenCalledWith(cart.total);
});
```

Red flags:

- Mocking internal collaborators
- Testing private methods
- Asserting on call counts/order
- Test breaks when refactoring without behavior change
- Test name describes HOW not WHAT
- Verifying through external means instead of interface

```typescript
// BAD: Bypasses interface to verify
test("createUser saves to database", async () => {
  await createUser({ name: "Alice" });
  const row = await db.query("SELECT * FROM users WHERE name = ?", ["Alice"]);
  expect(row).toBeDefined();
});

// GOOD: Verifies through interface
test("createUser makes user retrievable", async () => {
  const user = await createUser({ name: "Alice" });
  const retrieved = await getUser(user.id);
  expect(retrieved.name).toBe("Alice");
});
```

## System Boundary Contract Tests (MANDATORY for HTTP/gRPC client structs)

Tests that construct a struct directly and feed it to a fake client are **field-name
self-consistent** — if the JSON tag on the struct is wrong, the test still passes
because the fake produces the same wrong struct the consumer reads.

**This entire test class is invisible to the wire contract.** Field-name bugs ship
silently and only surface in production when the real upstream returns real JSON.

Every HTTP/gRPC client struct must have at least one test that exercises the
JSON boundary directly — feed a real-looking JSON byte string through
`json.Unmarshal` (or your language's equivalent) into the struct and assert the
fields populated.

```go
// BAD: Struct constructed in-test, fake returns it verbatim. Field-name mismatch
// vs upstream is invisible — the test loop never touches JSON.
func TestListRefundProgress_Bad(t *testing.T) {
    fake := &fakeClient{resp: &Resp{Steps: []*Step{
        {EventType: 6, Time: "...", Description: "Refund complete"},
    }}}
    out, _ := repo.ListProgress(ctx, "O-1")
    if out[0].Description != "Refund complete" { t.Fail() }
    // ← passes even if `json:"description"` is actually `json:"remark"` on the wire
}

// GOOD: Real JSON bytes → Unmarshal → assert fields populated.
// If a JSON tag drifts, this test catches it. Pair with a fixture taken from
// the upstream's canonical example, curl capture, or a sibling team's test fixture.
func TestRefundProgressStep_WireContract(t *testing.T) {
    raw := []byte(`{
        "id": 101,
        "description": "Cash auto refunded.",
        "time": "2026-04-21 12:00:00 +00:00",
        "is_current": true,
        "event_type": 6
    }`)
    var s RefundProgressStep
    if err := json.Unmarshal(raw, &s); err != nil { t.Fatal(err) }
    if s.Description != "Cash auto refunded." {
        t.Errorf("description: got %q (json tag wrong?)", s.Description)
    }
    if s.ID != 101 || s.EventType != 6 || !s.IsCurrent {
        t.Errorf("other fields: %+v", s)
    }
}
```

**Rules:**
- Fixture source order: canonical example > sibling team's test fixture > real curl
  capture > hand-typed from upstream handler code. Do NOT hand-type the fixture from
  your own struct definition — that defeats the point.
- One contract test per struct is enough; you're locking the wire format, not
  enumerating business cases.
- Construct-and-fake tests can complement, never replace, the contract test.
- If a struct has no contract test and the slice modified boundary code, the slice
  is not done.
