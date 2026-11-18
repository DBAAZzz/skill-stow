# Writing acceptance scenarios

Title it "subject + behavior + condition", and write the body as Given / When / Then:

- **Given** the starting state, only what matters for this scenario
- **When** one action
- **Then** the result the user can see, not internal state

> One scenario checks one thing. If you need two Thens, it's probably two scenarios.
