ALTER TABLE profiles DROP COLUMN IF EXISTS notification_opt_out;

ALTER TABLE blocks DROP CONSTRAINT IF EXISTS blocks_type_check;
ALTER TABLE blocks ADD CONSTRAINT blocks_type_check
  CHECK (type IN ('text', 'image', 'video', 'file', 'quiz'));

ALTER TABLE lessons DROP COLUMN IF EXISTS watch_threshold_percent;

DROP TABLE IF EXISTS learner_certificate;
DROP TABLE IF EXISTS course_announcement;
DROP TABLE IF EXISTS learner_assignment_grade;
DROP TABLE IF EXISTS learner_assignment_submission;
DROP TABLE IF EXISTS learner_quiz_score;
DROP TABLE IF EXISTS learner_quiz_attempt;
DROP TABLE IF EXISTS learner_lesson_progress;
DROP TABLE IF EXISTS learner_resume_position;
DROP TABLE IF EXISTS learner_course_access;
