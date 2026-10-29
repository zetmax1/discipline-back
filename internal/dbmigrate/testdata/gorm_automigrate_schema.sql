-- Schema produced by GORM AutoMigrate (phase-1 code) on Postgres 16, captured with
-- pg_dump --schema-only. Used to prove migration 00001 is a no-op on existing databases.


CREATE TABLE public.tasks (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    name text NOT NULL,
    category text NOT NULL,
    start_time text NOT NULL,
    end_time text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    distractions bigint DEFAULT 0 NOT NULL,
    energy bigint DEFAULT 3 NOT NULL,
    date text NOT NULL,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

CREATE SEQUENCE public.tasks_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.tasks_id_seq OWNED BY public.tasks.id;

CREATE TABLE public.users (
    id bigint NOT NULL,
    name text NOT NULL,
    email text NOT NULL,
    password text NOT NULL,
    role text DEFAULT 'user'::text NOT NULL,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

CREATE SEQUENCE public.users_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;

CREATE TABLE public.vocabulary_words (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    word text NOT NULL,
    language text NOT NULL,
    definition text NOT NULL,
    example text,
    synonym text,
    part_of_speech text,
    date text NOT NULL,
    learned boolean DEFAULT false,
    saved boolean DEFAULT false,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

CREATE SEQUENCE public.vocabulary_words_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.vocabulary_words_id_seq OWNED BY public.vocabulary_words.id;

ALTER TABLE ONLY public.tasks ALTER COLUMN id SET DEFAULT nextval('public.tasks_id_seq'::regclass);

ALTER TABLE ONLY public.users ALTER COLUMN id SET DEFAULT nextval('public.users_id_seq'::regclass);

ALTER TABLE ONLY public.vocabulary_words ALTER COLUMN id SET DEFAULT nextval('public.vocabulary_words_id_seq'::regclass);

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.vocabulary_words
    ADD CONSTRAINT vocabulary_words_pkey PRIMARY KEY (id);

CREATE INDEX idx_tasks_date ON public.tasks USING btree (date);

CREATE INDEX idx_tasks_user_id ON public.tasks USING btree (user_id);

CREATE UNIQUE INDEX idx_users_email ON public.users USING btree (email);

CREATE INDEX idx_vocabulary_words_date ON public.vocabulary_words USING btree (date);

CREATE INDEX idx_vocabulary_words_user_id ON public.vocabulary_words USING btree (user_id);


