-- Users table (people invited to vote)
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    invitation_id INT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Insert default admin user
INSERT INTO users (name, invitation_id) VALUES ('admin', NULL);

-- Invitations table (one-time use invite links)
CREATE TABLE invitations (
    id SERIAL PRIMARY KEY,
    token VARCHAR(255) UNIQUE NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    redeemed_at TIMESTAMP,
    redeemed_by INT REFERENCES users(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Votings table (main voting event, e.g., "Best of 2025")
CREATE TABLE votings (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    created_by INT NOT NULL REFERENCES users(id),
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Categories table (subcategories within a voting, e.g., "Personality of the Year")
CREATE TABLE categories (
    id SERIAL PRIMARY KEY,
    voting_id INT NOT NULL REFERENCES votings(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Options table (candidates/names to vote for)
CREATE TABLE options (
    id SERIAL PRIMARY KEY,
    category_id INT NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Votes table (individual votes cast)
CREATE TABLE votes (
    id SERIAL PRIMARY KEY,
    voting_id INT NOT NULL REFERENCES votings(id) ON DELETE CASCADE,
    category_id INT NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    option_id INT NOT NULL REFERENCES options(id) ON DELETE CASCADE,
    user_id INT NOT NULL REFERENCES users(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(voting_id, category_id, user_id)
);

-- Create indexes for better query performance
CREATE INDEX idx_votes_user_id ON votes(user_id);
CREATE INDEX idx_votes_voting_id ON votes(voting_id);
CREATE INDEX idx_votes_category_id ON votes(category_id);
CREATE INDEX idx_categories_voting_id ON categories(voting_id);
CREATE INDEX idx_options_category_id ON options(category_id);
CREATE INDEX idx_invitations_token ON invitations(token);
