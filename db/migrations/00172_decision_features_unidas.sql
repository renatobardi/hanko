-- +goose Up
-- Features do decision model: a lista COMPLETA vigente. A 00151 (M19) acrescentou fast_response e a 00160 (M20),
-- que roda depois, recriou a constraint com card_summary mas sem fast_response; esta migration as une.
ALTER TABLE decision_features DROP CONSTRAINT decision_features_feature_check;
ALTER TABLE decision_features ADD CONSTRAINT decision_features_feature_check
    CHECK (feature IN ('triage', 'phase_classifier', 'name_summary', 'fast_response', 'card_summary'));
ALTER TABLE decision_invocations DROP CONSTRAINT decision_invocations_feature_check;
ALTER TABLE decision_invocations ADD CONSTRAINT decision_invocations_feature_check
    CHECK (feature IN ('triage', 'phase_classifier', 'name_summary', 'fast_response', 'card_summary'));

-- +goose Down
-- Volta ao estado da 00160 (sem fast_response).
ALTER TABLE decision_invocations DROP CONSTRAINT decision_invocations_feature_check;
ALTER TABLE decision_invocations ADD CONSTRAINT decision_invocations_feature_check
    CHECK (feature IN ('triage', 'phase_classifier', 'name_summary', 'card_summary'));
ALTER TABLE decision_features DROP CONSTRAINT decision_features_feature_check;
DELETE FROM decision_features WHERE feature = 'fast_response';
ALTER TABLE decision_features ADD CONSTRAINT decision_features_feature_check
    CHECK (feature IN ('triage', 'phase_classifier', 'name_summary', 'card_summary'));
