CREATE TABLE file_translations (
    fingerprint TEXT NOT NULL REFERENCES blobs(fingerprint), -- 原文的内容指纹,指向 blobs 里的内容
    language TEXT NOT NULL,                                  -- 译成的语言,BCP 47 语言标签,如 zh、en
    translation TEXT NOT NULL,                               -- 译文
    translated_at TEXT NOT NULL DEFAULT '',                  -- 翻译时间,RFC3339Nano
    PRIMARY KEY(fingerprint, language)
);
