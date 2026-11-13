-- Marketplace, accommodation, and featured eSIM schema.
-- Production schema is owned by the TanStack admin dashboard; this mirror
-- keeps the Go integration test database in sync.

CREATE TABLE IF NOT EXISTS partner_businesses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    category TEXT NOT NULL CHECK (category IN ('experiences', 'food_drink', 'transport', 'shopping', 'services', 'connectivity')),
    tagline TEXT,
    short_description TEXT NOT NULL,
    full_description TEXT,
    county TEXT,
    locality TEXT,
    service_area TEXT,
    address TEXT,
    location_lat NUMERIC(10, 8),
    location_lng NUMERIC(11, 8),
    contact_email TEXT,
    contact_phone TEXT,
    redirect_url TEXT NOT NULL,
    redirect_label TEXT DEFAULT 'Visit partner',
    disclosure TEXT,
    image_url TEXT,
    image_alt TEXT,
    image_credit TEXT,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    is_featured BOOLEAN NOT NULL DEFAULT false,
    display_order INTEGER NOT NULL DEFAULT 0 CHECK (display_order >= 0 AND display_order <= 10000),
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS partner_businesses_category_status_idx
    ON partner_businesses (category, status);
CREATE INDEX IF NOT EXISTS partner_businesses_county_status_idx
    ON partner_businesses (county, status);
CREATE INDEX IF NOT EXISTS partner_businesses_status_featured_order_idx
    ON partner_businesses (status, is_featured, display_order);

CREATE TABLE IF NOT EXISTS accommodation_properties (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    tier TEXT NOT NULL CHECK (tier IN ('luxury', 'midrange', 'budget')),
    property_type TEXT NOT NULL CHECK (property_type IN ('lodge', 'camp', 'hotel', 'hostel', 'guesthouse', 'apartment', 'villa')),
    short_description TEXT NOT NULL,
    full_description TEXT,
    county TEXT NOT NULL,
    locality TEXT,
    address TEXT,
    location_lat NUMERIC(10, 8),
    location_lng NUMERIC(11, 8),
    price_from_kes INTEGER CHECK (price_from_kes IS NULL OR price_from_kes >= 0),
    price_note TEXT,
    amenities TEXT[] NOT NULL DEFAULT '{}',
    highlights TEXT[] NOT NULL DEFAULT '{}',
    booking_url TEXT,
    direct_url TEXT,
    disclosure TEXT,
    contact_email TEXT,
    contact_phone TEXT,
    image_url TEXT,
    image_alt TEXT,
    image_credit TEXT,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    is_featured BOOLEAN NOT NULL DEFAULT false,
    display_order INTEGER NOT NULL DEFAULT 0 CHECK (display_order >= 0 AND display_order <= 10000),
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS accommodation_properties_tier_status_idx
    ON accommodation_properties (tier, status);
CREATE INDEX IF NOT EXISTS accommodation_properties_county_status_idx
    ON accommodation_properties (county, status);
CREATE INDEX IF NOT EXISTS accommodation_properties_status_featured_order_idx
    ON accommodation_properties (status, is_featured, display_order);

CREATE TABLE IF NOT EXISTS esim_providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    headline TEXT,
    description TEXT NOT NULL,
    coverage_note TEXT,
    price_label TEXT,
    redirect_url TEXT NOT NULL,
    redirect_label TEXT DEFAULT 'Get connected',
    disclosure TEXT,
    logo_url TEXT,
    logo_alt TEXT,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    is_featured BOOLEAN NOT NULL DEFAULT false,
    display_order INTEGER NOT NULL DEFAULT 0 CHECK (display_order >= 0 AND display_order <= 10000),
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS esim_providers_one_featured_idx
    ON esim_providers ((is_featured))
    WHERE is_featured = true AND status = 'published';

CREATE TABLE IF NOT EXISTS marketplace_redirects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type TEXT NOT NULL CHECK (entity_type IN ('partner_business', 'accommodation', 'esim_provider')),
    entity_id UUID NOT NULL,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    source TEXT,
    platform TEXT CHECK (platform IN ('ios', 'android', 'web')),
    created_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS marketplace_redirects_entity_idx
    ON marketplace_redirects (entity_type, entity_id, created_at);
