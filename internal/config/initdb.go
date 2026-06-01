package config

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const schema = `
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS vaomiera (
  id VARCHAR(20) PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  short VARCHAR(30) NOT NULL,
  name_fr VARCHAR(60),
  icon VARCHAR(5),
  color VARCHAR(10)
);

CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  nom VARCHAR(100) NOT NULL,
  prenom VARCHAR(100) NOT NULL,
  email VARCHAR(150) UNIQUE NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  date_naissance DATE,
  role VARCHAR(20) NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin', 'super')),
  is_active BOOLEAN DEFAULT TRUE,
  avatar_url VARCHAR(255),
  created_at TIMESTAMP DEFAULT NOW(),
  updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_vaomiera (
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  vaomiera_id VARCHAR(20) REFERENCES vaomiera(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, vaomiera_id)
);

CREATE TABLE IF NOT EXISTS songs (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  numero VARCHAR(20),
  title VARCHAR(200) NOT NULL,
  composer VARCHAR(150),
  category VARCHAR(50),
  tonalite VARCHAR(10),
  lang VARCHAR(5) DEFAULT 'mg',
  paroles TEXT,
  file_url VARCHAR(255),
  file_size INTEGER,
  file_type VARCHAR(10),
  created_by UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMP DEFAULT NOW(),
  updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS song_favorites (
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  song_id UUID REFERENCES songs(id) ON DELETE CASCADE,
  created_at TIMESTAMP DEFAULT NOW(),
  PRIMARY KEY (user_id, song_id)
);

CREATE TABLE IF NOT EXISTS notifications (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  title VARCHAR(200) NOT NULL,
  body TEXT NOT NULL,
  from_user UUID REFERENCES users(id) ON DELETE SET NULL,
  vaomiera_id VARCHAR(20) REFERENCES vaomiera(id),
  status VARCHAR(20) DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
  created_at TIMESTAMP DEFAULT NOW(),
  updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS notification_reads (
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  notification_id UUID REFERENCES notifications(id) ON DELETE CASCADE,
  read_at TIMESTAMP DEFAULT NOW(),
  PRIMARY KEY (user_id, notification_id)
);

CREATE TABLE IF NOT EXISTS events (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  title VARCHAR(200) NOT NULL,
  location VARCHAR(200),
  event_date DATE NOT NULL,
  event_time TIME,
  tag VARCHAR(50),
  color VARCHAR(10),
  description TEXT,
  created_by UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMP DEFAULT NOW(),
  updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS sync_log (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  table_name VARCHAR(50) NOT NULL,
  record_id UUID NOT NULL,
  action VARCHAR(20) NOT NULL CHECK (action IN ('INSERT', 'UPDATE', 'DELETE')),
  payload JSONB,
  synced_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_songs_category ON songs(category);
CREATE INDEX IF NOT EXISTS idx_songs_title ON songs(title);
CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_date ON events(event_date);
CREATE INDEX IF NOT EXISTS idx_sync_log_table ON sync_log(table_name, synced_at DESC);

CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_users_updated') THEN
    CREATE TRIGGER trg_users_updated BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION update_updated_at();
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_songs_updated') THEN
    CREATE TRIGGER trg_songs_updated BEFORE UPDATE ON songs FOR EACH ROW EXECUTE FUNCTION update_updated_at();
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_notifs_updated') THEN
    CREATE TRIGGER trg_notifs_updated BEFORE UPDATE ON notifications FOR EACH ROW EXECUTE FUNCTION update_updated_at();
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_events_updated') THEN
    CREATE TRIGGER trg_events_updated BEFORE UPDATE ON events FOR EACH ROW EXECUTE FUNCTION update_updated_at();
  END IF;
END $$;
`

const seedVaomiera = `
INSERT INTO vaomiera (id, name, short, name_fr, icon, color) VALUES
  ('hira',      'Vaomieran''ny Hira',                     'Hira',      'Chants',           '♪', '#261BBE'),
  ('serasera',  'Vaomieran''ny Serasera',                 'Serasera',  'Communication',    '◉', '#F76A2C'),
  ('fitaovana', 'Vaomieran''ny Fitaovana',                'Fitaovana', 'Matériel',         '▣', '#5B4FD9'),
  ('talenta',   'Vaomieran''ny Talenta sy Fampandrosoana','Talenta',   'Talents',          '✦', '#F5B915'),
  ('filaminana','Vaomieran''ny Filaminana',               'Filaminana','Ordre',            '◇', '#7A5AE0'),
  ('aim',       'Vaomieran''ny Aim-panahy sy Sosialy',    'Aim-panahy','Spirituel/Social', '✚', '#E63E5C'),
  ('sport',     'Vaomieran''ny Sport',                    'Sport',     'Sport',            '◆', '#11A37B'),
  ('vola',      'Vaomieran''ny Vola',                     'Vola',      'Trésorerie',       '$', '#1A1280')
ON CONFLICT (id) DO NOTHING;
`

func InitDB(ctx context.Context, pool *pgxpool.Pool) error {
	log.Println("Initialisation de la base de données AKRIFI...")

	if _, err := pool.Exec(ctx, schema); err != nil {
		return err
	}
	log.Println("✓ Schéma créé")

	pool.Exec(ctx, `ALTER TABLE songs ADD COLUMN IF NOT EXISTS paroles TEXT`)
	pool.Exec(ctx, `ALTER TABLE songs ALTER COLUMN numero DROP NOT NULL`)
	pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS reset_code_hash VARCHAR(255)`)
	pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS reset_code_expires_at TIMESTAMP`)
	log.Println("✓ Migrations appliquées")

	if _, err := pool.Exec(ctx, seedVaomiera); err != nil {
		return err
	}
	log.Println("✓ Vaomiera insérées")

	adminPwd := os.Getenv("ADMIN_PASSWORD")
	if adminPwd == "" {
		adminPwd = "Admin@2026"
		log.Println("⚠️  ADMIN_PASSWORD non défini, utilisation du mot de passe par défaut — changez-le en production !")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(adminPwd), 12)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO users (nom, prenom, email, password_hash, role)
		VALUES ('AKRIFI', 'Super Admin', 'admin@akrifi.mg', $1, 'super')
		ON CONFLICT (email) DO NOTHING
	`, string(hash))
	if err != nil {
		return err
	}
	log.Println("✓ Super Admin initialisé : admin@akrifi.mg")
	log.Println("\n✅ Base de données AKRIFI initialisée avec succès !")
	return nil
}
