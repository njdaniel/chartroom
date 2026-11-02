# Little Wars on Chartroom: the adaptation (proposed)

**For:** M1's first ruleset, `rulesets/little_wars`.
**Source:** H. G. Wells, *Little Wars* (1913), public domain ([Project Gutenberg #3691](https://www.gutenberg.org/ebooks/3691)), chapter III, "The Game".
**License of our package:** CC-BY-4.0, with attribution to Wells. The rules text is public domain; our adaptation and its numbers are our own.

Wells's game is played on a floor with toy soldiers and spring-loaded cannons that fire wooden cylinders. A figure the shot knocks over is dead. Everything else in the rules is measured and counted, so it carries over exactly. Firing can't, so it needs a digital replacement, and that's the main decision here. Rules content is an ADR-000 sign-off item (7), so this document asks for sign-off on each adaptation.

Quotes are from chapter III. "Wells:" is his rule. "Chartroom:" is what the package does.

## What carries over exactly

| Rule | Wells | Chartroom |
| --- | --- | --- |
| Moves alternate | Players take alternate moves; they "toss for the first move". | The first move is decided by a logged `1d2`. |
| Infantry | "may be moved a foot or any less distance at each move" | Up to 12 in per move, measured centre to centre from where it started the move. |
| Cavalry | "two feet or any less distance" | Up to 24 in. |
| Guns First | "a player must move or fire his guns if he wants to do so, before moving his men. To this rule of 'Guns First' there is to be no exception." | Each move has a `guns` phase, then a `men` phase. Gun actions are refused in `men`. |
| No early fire | "Guns must not be fired before the second move of the first player" | Firing is refused before the first player's second move. |
| Gun in action | needs "at least four men of its own side within six inches of it", or it "can neither be moved nor fired" | The same, measured edge to edge. Prisoners and disarmed men don't count. |
| Gun moves or fires | "can either be moved or fired at each move, but not both" | The same. |
| Moving a gun | at least four of its men "must move up with it"; "two feet" if at least four are cavalry, otherwise "one foot" | One `move_gun` action moves the gun and four or more crew together, up to 24 in with four or more cavalry, otherwise 12 in. |
| Shots per move | "as many as four shots in each move" | Up to 4 `fire` actions per gun per move. |
| Melee trigger | at the end of a move, men of the moving side "in contact" with enemy men; contact is "one-eighth of an inch or closer" | At the end of a move, bases 1/8 in apart or less are in contact. |
| Who fights | "All the men in contact, and any other men within six inches of the men in contact" | The same, edge to edge, both sides. |
| Equal numbers | "all the men on both sides are killed" | The same. |
| Isolated inferior force | "as many men become prisoners as the inferior force is less in numbers than the superior force, and the rest kill each a man and are killed" (9 against 11: 2 prisoners, 7 dead each) | The same. Wells's 9-against-11 example is a package test. |
| Supported inferior force | "each man of the inferior force kills a man of the superior force and is himself killed" | The same. |
| Isolation | less than half its number of own men "within a move of it" | See decision 4. |
| Escort | "one man within six inches of any number of prisoners up to seven can escort these prisoners" | Checked at the end of every move. |
| Liberation | when the escort within six inches dies; freed men can't be moved until the next move, and "cannot fight until they are rearmed" at "the back line of their own side" | The same: `armed: false` until the man reaches his own back line. |
| Gun capture | no man of its side within six inches, and at least four enemy men "have moved up to it and have passed its wheel axis" | The same, with the wheel axis being the gun base's centre line across its facing. |
| Surrender | "Any isolated body may hoist the white flag and surrender at any time." | A `surrender` action for a selected isolated group. |
| Army size | equal forces; or "150" points with infantry 1, cavalry 1½, gun 10 | Setup offers Wells's example army (60 infantry, 40 cavalry, 3 guns: 150 points), or a custom one up to 150 points, the same for both sides. |
| Victory | Fight to a Finish: "kill, capture, or drive over his back line the whole of the enemy's force" | The same. |
| Scoring | "100 for the victory, and 10 for every gun you hold or are in a position to take, 1½ for every cavalry-man, 1 for every infantry-man still alive and uncaptured, ½ for every man of yours prisoner in the hands of the enemy, and ½ for every prisoner you have taken" | The same, shown live and final in `game.ended`. |
| Draw | "both forces are reduced below fifteen men" (ten if fought with fewer than 50 a side) | The same. |

## Decisions for sign-off

### 1. Firing (the one real change)

Wells has no dice in firing: the physical shot decides. Chartroom needs a model that keeps what makes his firing work. Aiming matters, results scatter, a shot flies over men in front of the gun (his crews "lie down" while guns fire over them), and it can knock down more than one figure, friend or foe.

**Proposal.** The player aims each shot at a point on the table, at any range (Wells sets no limit).
1. **Scatter.** Roll `1d6-1d6` for the angle error, 2° per point (−10° to +10°), and `1d6-1d6` for the range error, 5% of the range per point (−25% to +25%). The result is where the shot lands.
2. **Skid.** The shot then skids on along the same line for `1d6` inches.
3. **Casualties.** Every man (infantry or cavalry, either side) whose base the skid path touches, ¾ in wide, is killed, up to 3 men. Men under the flight path before the landing point are safe. A gun in the skid path isn't harmed.
4. **Guns stay pointed.** After its last shot the gun is left pointing at its last aim point, and two crew are placed behind its trail, as Wells requires.

Every roll is logged and labelled, such as "Gun 2, shot 3: scatter", and a `rule.noted` line says who fell.

These numbers are a first guess at Wells's spring guns, which were accurate at a few feet and erratic across a room. They are exactly what M5's simulator is for. *Alternatives:* a straight hit-roll per target (simple, but it loses aiming and friendly fire), or a 5e-style area template (closer to his Kriegspiel shells, but not to the main game).

### 2. Who chooses casualties

Wells: "the player who has just completed the move ... decides, when there is any choice, which men in the melee, both of his own and of his antagonist, shall die and which shall be prisoners." **Proposal:** at the end of a move with a melee, the moving side gets a `melee` phase. It picks the dead and the prisoners for each melee, with a suggested default (the men nearest the contact first), and confirms. That keeps his rule and exercises a pending decision in the floor, which the engine will need for 5e reactions. *Alternative:* always use the default, which is simpler but drops a real choice.

### 3. The table and the Country

Wells's Country is a floor with books for hills and toy buildings. M1 has no terrain engine yet (that arrives with the spatial model and M3). **Proposal:** M1 plays on an open field, 72 × 48 in by default (6 × 4 ft, so infantry cross it in six moves), with the back lines on the long edges. The map image is decoration only. Buildings, hills, and line of sight come with terrain.

### 4. "Within a move"

Wells defines isolation as less than half your number within "a move", without saying whose move: infantry move 12 in and cavalry 24 in. **Proposal:** use 12 in for everyone. It's the stricter reading, and it doesn't make cavalry-heavy forces hard to isolate.

### 5. Deployment

Wells: the first player places his men "along his back line", then the second player does the same. Men placed in front of or behind it "count ... as if they touched the back line at its nearest point". **Proposal:** setup lays both armies out in blocks within 4 in of their back lines. In a deployment phase, each side can rearrange its men within that strip, first player first, then the game starts. The "curtain" variant, where both deploy unseen at once, needs hidden information and waits for fog of war.

### 6. Moving many men

A 150-point army is about 100 figures. **Proposal:** the client lets you select many men (shift-click or drag a box) and move them together. The server checks each man's own distance, so a block that's partly cavalry and partly infantry is limited by its infantry.

### 7. What's left out of M1

Wells's per-move time limits ("about a minute ... for moving 30 men"), the Blow at the Rear and Defensive Game variants, and everything in chapter V and the Kriegspiel appendix. The variants and the timer are easy additions later. The appendix is a different game.

## Tests the package ships

Each rule above gets a scenario in `tests/`. Some of them:
- Infantry moving 12.1 in is refused; 12 in is accepted.
- A gun with three crew can't fire. With four it can, and it can't then move that move.
- Firing in the `men` phase is refused.
- Wells's melee example: 9 against 11, isolated, gives 2 prisoners and 7 dead each. Equal numbers kill everyone. Supported inferior forces trade one for one.
- An escort guards up to seven prisoners. Killing the escort frees them, unarmed, until they reach their back line.
- Gun capture needs four enemy men past the wheel axis and no defenders within 6 in.
- Fight to a Finish scoring for a known end state, and the draw rule at fewer than 15 men each.
- With a fixed seed, a shot lands at a known point and kills the men under its skid, including a friendly one.
