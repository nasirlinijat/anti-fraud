-- Card checks the payment gateway reports for the purchase, as the score
-- request carries them. NULL means the request did not send the result.
ALTER TABLE purchases
    ADD COLUMN cvv_result      text CHECK (cvv_result IN ('match', 'mismatch', 'not_checked')),
    ADD COLUMN avs_result      text CHECK (avs_result IN ('match', 'partial', 'mismatch', 'not_checked')),
    ADD COLUMN three_ds_result text CHECK (three_ds_result IN ('authenticated', 'attempted', 'failed', 'not_enrolled'));
