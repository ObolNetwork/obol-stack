@bdd
Feature: {Feature Name}
  As a {actor/role}
  I want {capability}
  So that {benefit}

  # References: SPEC Section {N} ({Section Name}), B&E Section {M} ({Section Name})

  Background:
    Given {common precondition for all scenarios}

  @phase1 @fast
  Scenario: {Happy path scenario name}
    Given {context/setup}
    When {action the user takes}
    Then {expected outcome}
    And {additional assertion}

  @phase1 @fast
  Scenario: {Validation/error scenario name}
    Given {context/setup}
    When {action with invalid input or edge case}
    Then {expected error handling}
    And {system remains in valid state}

  @phase1
  Scenario Outline: {Parameterized scenario name}
    Given {context with <parameter>}
    When {action with <input>}
    Then {expected <outcome>}

    Examples:
      | parameter | input | outcome |
      | {val1}    | {val1} | {val1} |
      | {val2}    | {val2} | {val2} |
