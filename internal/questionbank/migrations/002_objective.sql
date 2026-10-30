-- Objective answers have their own immutable contract; they are not source files.
CREATE TABLE IF NOT EXISTS objective_questions (
 question_key TEXT PRIMARY KEY,
 question_json TEXT NOT NULL,
 resources_json TEXT NOT NULL,
 resource_complete INTEGER NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS objective_answers (
 id INTEGER PRIMARY KEY,
 question_key TEXT NOT NULL REFERENCES objective_questions(question_key),
 answer TEXT NOT NULL,
 answer_hash TEXT NOT NULL,
 source TEXT NOT NULL,
 model TEXT NOT NULL,
 created_at TEXT NOT NULL,
 UNIQUE(question_key,answer_hash)
);
CREATE TABLE IF NOT EXISTS objective_answer_verifications (
 id INTEGER PRIMARY KEY,
 answer_id INTEGER NOT NULL REFERENCES objective_answers(id),
 verified INTEGER NOT NULL,
 evidence_json TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS objective_answers_question ON objective_answers(question_key);
CREATE INDEX IF NOT EXISTS objective_answer_verifications_answer ON objective_answer_verifications(answer_id,verified);
INSERT OR IGNORE INTO schema_migrations(version) VALUES(2);
