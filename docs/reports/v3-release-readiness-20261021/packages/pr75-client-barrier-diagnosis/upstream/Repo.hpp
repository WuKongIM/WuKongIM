/*
 * Copyright (C) 2018 Red Hat, Inc.
 *
 * Licensed under the GNU Lesser General Public License Version 2.1
 *
 * This library is free software; you can redistribute it and/or
 * modify it under the terms of the GNU Lesser General Public
 * License as published by the Free Software Foundation; either
 * version 2.1 of the License, or (at your option) any later version.
 *
 * This library is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
 * Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public
 * License along with this library; if not, write to the Free Software
 * Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA
 */

#ifndef _LIBDNF_REPO_HPP
#define _LIBDNF_REPO_HPP

#define MODULEMD

#include "../conf/ConfigRepo.hpp"
#include "../error.hpp"
#include "../hy-types.h"

#include <memory>
#include <stdexcept>

namespace libdnf {

class LrException : public std::runtime_error {
public:
    LrException(int code, const char * msg) : runtime_error(msg), code(code) {}
    LrException(int code, const std::string & msg) : runtime_error(msg), code(code) {}
    int getCode() const noexcept { return code; }
private:
    int code;
};

/**
* @class RepoCB
*
* @brief Base class for Repo callbacks
*
* User implements repo callbacks by inheriting this class and overriding its methods.
*/
class RepoCB {
public:
    enum class FastestMirrorStage {
        INIT, /*!<
            Fastest mirror detection just started.
            ptr is NULL*/

        CACHELOADING, /*!<
            ptr is (char *) pointer to string with path to the cache file.
            (Do not modify or free the string). */

        CACHELOADINGSTATUS, /*!<
            if cache was loaded successfully, ptr is NULL, otherwise
            ptr is (char *) string with error message.
            (Do not modify or free the string) */

        DETECTION, /*!<
            Detection (pinging) in progress.
            If all data was loaded from cache, this stage is skiped.
            ptr is pointer to long. This is the number of how much
            mirrors have to be "pinged" */

        FINISHING, /*!<
            Detection is done, sorting mirrors, updating cache, etc.
            ptr is NULL */

        STATUS, /*!<
            The very last invocation of fastest mirror callback.
            If fastest mirror detection was successful ptr is NULL,
            otherwise ptr contain (char *) string with error message.
            (Do not modify or free the string) */
    };

    virtual void start(const char *what) {}
    virtual void end() {}
    virtual int progress(double totalToDownload, double downloaded);
    virtual void fastestMirror(FastestMirrorStage stage, const char *msg);
    virtual int handleMirrorFailure(const char *msg, const char *url, const char *metadata);
    virtual bool repokeyImport(const std::string & id, const std::string & userId,
                               const std::string & fingerprint, const std::string & url, long int timestamp);
    virtual ~RepoCB() = default;
};

/**
* @class Repo
*
* @brief Package repository
*
* Represents a repository used to download packages.
* Remote metadata is cached locally.
*
*/
struct Repo {
public:

    enum class Type {
        AVAILABLE,
        SYSTEM,
        COMMANDLINE
    };

    enum class SyncStrategy {
        // use the local cache even if it's expired. download if there's no cache.
        LAZY = 1,
        // use the local cache, even if it's expired, never download.
        ONLY_CACHE = 2,
        // try the cache, if it is expired download new md.
        TRY_CACHE = 3
    };


    /**
    * @brief Verify repo ID
    *
    * @param id repo ID to verify
    * @return   index of the first invalid character in the repo ID (if present) or -1
    */
    static int verifyId(const std::string & id);

    /**
    * @brief Construct the Repo object
    *
    * @param id     repo ID to use
    * @param conf   configuration to use
    */
    Repo(const std::string & id, std::unique_ptr<ConfigRepo> && conf, Repo::Type type = Repo::Type::AVAILABLE);

    Repo & operator =(Repo && repo) = delete;

    void setCallbacks(std::unique_ptr<RepoCB> && callbacks);

    /**
    * @brief Verify repo object configuration
    *
    * Will throw exception if Repo has no mirror or baseurl set or if Repo type is unsupported.
    */
    void verify() const;
    ConfigRepo * getConfig() noexcept;
    const std::string & getId() const noexcept;
    void enable();
    void disable();
    bool isEnabled() const;
    bool isLocal() const;
    /**
     * @brief if the repository is local, returns the baseurl in form of a
     * local filesystem path ("file://" is stripped from it and the URL is
     * decoded).
     *
     * @return A local filesystem path
     */
    std::string getLocalBaseurl() const;
    /**
    * @brief Initialize the repo with metadata
    *
    * Fetches new metadata from the origin or just reuses local cache if still valid.
    *
    * @return true if fresh metadata were downloaded, false otherwise.
    */
    bool load();
    bool loadCache(bool throwExcept, bool ignoreMissing=false);
    void downloadMetadata(const std::string & destdir);
    bool getUseIncludes() const;
    void setUseIncludes(bool enabled);
    bool getLoadMetadataOther() const;
    void setLoadMetadataOther(bool value);
    int getCost() const;
    int getPriority() const;
    std::string getCompsFn();  // this is temporarily made public for DNF compatibility
#ifdef MODULEMD
    std::string getModulesFn(); // temporary made public
#endif
    const std::string & getRevision() const;
    int getAge() const;

    /**
    * @brief Ask for additional repository metadata type to download
    *
    * given metadata are appended to the default metadata set when repository is downloaded
    *
    * @param metadataType metadata type (filelists, other, productid...)
    */
    void addMetadataTypeToDownload(const std::string &metadataType);

    /**
    * @brief Stop asking for this additional repository metadata type
    *
    * given metadata_type is no longer downloaded by default
    * when this repository is downloaded.
    *
    * @param metadataType metadata type (filelists, other, productid...)
    */
    void removeMetadataTypeFromDownload(const std::string &metadataType);

    /**
    * @brief Return path to the particular downloaded repository metadata in cache
    *
    * @param metadataType metadata type (filelists, other, productid...)
    *
    * @return file path or empty string in case the requested metadata does not exist
    */
    std::string getMetadataPath(const std::string &metadataType);

    /**
    * @brief Return content of the particular downloaded repository metadata
    *
    * Content of compressed metadata file is returned uncompressed
    *
    * @param metadataType metadata type (filelists, other, productid...)
    *
    * @return content of metadata file or empty string in case the requested metadata does not exist
    */
    std::string getMetadataContent(const std::string &metadataType);

    /**
    * @brief Mark whatever is in the current cache expired.
    *
    * This repo instance will alway try to fetch a fresh metadata after this
    * method is called.
    */
    void expire();

    /**
    * @brief Return whether the cached metadata is expired.
    *
    * @return bool
    */
    bool isExpired() const;

    /**
    * @brief Get the number of seconds after which the cached metadata will expire.
    *
    * Negative number means the metadata has expired already.
    *
    * @return Seconds to expiration
    */
    int getExpiresIn() const;

    /**
    * @brief Returns whether the metadata was loaded from the origin, not from cache
    *
    * @return bool
    */
    bool fresh();

    void setMaxMirrorTries(int maxMirrorTries);
    int getTimestamp() const;
    int getMaxTimestamp();

    /**
    * @brief Try to preserve remote side timestamps
    *
    * When set to true the underlying librepo is asked to make an attempt to set the timestamps
    * of the local downloaded files (repository metadata and packages) to match those from
    * the remote files.
    * This feature is by default switched off.
    *
    * @param preserveRemoteTime true - use remote file timestamp, false - use the current time
    */
    void setPreserveRemoteTime(bool preserveRemoteTime);
    bool getPreserveRemoteTime() const;

    const std::vector<std::string> & getContentTags();
    const std::vector<std::pair<std::string, std::string>> & getDistroTags();

    /**
    * @brief Get list of relative locations of metadata files inside the repo
    *
    * e.g. [('primary', 'repodata/primary.xml.gz'), ('filelists', 'repodata/filelists.xml.gz')...]
    *
    * @return vector of (metadata_type, location) string pairs
    */
    const std::vector<std::pair<std::string, std::string>> getMetadataLocations() const;

    std::string getCachedir() const;
    void setRepoFilePath(const std::string & path);
    const std::string & getRepoFilePath() const noexcept;
    void setSyncStrategy(SyncStrategy strategy);
    SyncStrategy getSyncStrategy() const noexcept;
    void downloadUrl(const char * url, int fd);

    /**
    * @brief Set http headers.
    *
    * Example:
    * {"User-Agent: Agent007", "MyMagicHeader: I'm here", nullptr}
    *
    * @param headers nullptr terminated array of C strings
    */
    void setHttpHeaders(const char * headers[]);

    /**
    * @brief Get array of added/changed/removed http headers.
    *
    * @return nullptr terminated array of C strings
    */
    const char * const * getHttpHeaders() const;
    std::vector<std::string> getMirrors() const;

    void setSubstitutions(const std::map<std::string, std::string> & substitutions);

    ~Repo();

    class Impl;
private:
    friend struct PackageTarget;
    friend Impl * repoGetImpl(Repo * repo);
    std::unique_ptr<Impl> pImpl;
};

struct Downloader {
public:
    static void downloadURL(ConfigMain * cfg, const char * url, int fd);
};

/**
* @class PackageTargetCB
*
* @brief Base class for PackageTarget callbacks
*
* User implements PackageTarget callbacks by inheriting this class and overriding its methods.
*/
class PackageTargetCB {
public:
    /** Transfer status codes */
    enum class TransferStatus {
        SUCCESSFUL,
        ALREADYEXISTS,
        ERROR
    };

    virtual int end(TransferStatus status, const char * msg);
    virtual int progress(double totalToDownload, double downloaded);
    virtual int mirrorFailure(const char *msg, const char *url);
    virtual ~PackageTargetCB() = default;
};

/**
* @class PackageTarget
*
* @brief Wraps librepo PackageTarget
*/
struct PackageTarget {
public:
    /** Enum of supported checksum types.
    * NOTE! This enum guarantee to be sorted by "hash quality"
    */
    enum class ChecksumType {
        UNKNOWN,
        MD5,        /*    The most weakest hash */
        SHA1,       /*  |                       */
        SHA224,     /*  |                       */
        SHA256,     /*  |                       */
        SHA384,     /* \|/                      */
        SHA512,     /*    The most secure hash  */
    };

    static ChecksumType checksumType(const std::string & name);
    static void downloadPackages(std::vector<PackageTarget *> & targets, bool failFast);

    PackageTarget(Repo * repo, const char * relativeUrl, const char * dest, int chksType,
                  const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
                  int64_t byteRangeStart, int64_t byteRangeEnd, PackageTargetCB * callbacks);
    PackageTarget(ConfigMain * cfg, const char * relativeUrl, const char * dest, int chksType,
                  const char * chksum, int64_t expectedSize, const char * baseUrl, bool resume,
                  int64_t byteRangeStart, int64_t byteRangeEnd, PackageTargetCB * callbacks,
                  const char * httpHeaders[] = nullptr);
    ~PackageTarget();

    PackageTargetCB * getCallbacks();
    const char * getErr();
private:
    class Impl;
    std::unique_ptr<Impl> pImpl;
};

struct LibrepoLog {
public:
    static long addHandler(const std::string & filePath, bool debug = false);
    static void removeHandler(long uid);
    static void removeAllHandlers();
};

class RepoError : public Error {
public:
    RepoError(const std::string & what) : Error(what) {}
};

}

#endif
