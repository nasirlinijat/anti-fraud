-- The main model's own score, before it is blended with the player's earlier
-- scores and combined with other models. The blend averages these, so a
-- blended score never feeds the next blend.
ALTER TABLE decisions
    ADD COLUMN model_score_unblended double precision
    CHECK (model_score_unblended BETWEEN 0 AND 1);
