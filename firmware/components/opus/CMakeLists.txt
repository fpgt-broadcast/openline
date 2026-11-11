# libopus built from upstream source as an ESP-IDF component (float build;
# the ESP32-S3 has a single-precision FPU).
#
# Source location, first match wins:
#   1. environment variable OPUS_SRC_DIR
#   2. firmware/components/opus/libopus   (git clone --depth 1 -b v1.5.2 https://github.com/xiph/opus libopus)
if(DEFINED ENV{OPUS_SRC_DIR})
    set(OPUS_DIR "$ENV{OPUS_SRC_DIR}")
else()
    set(OPUS_DIR "${CMAKE_CURRENT_LIST_DIR}/libopus")
endif()
if(NOT EXISTS "${OPUS_DIR}/opus_sources.mk")
    message(FATAL_ERROR "libopus source not found at ${OPUS_DIR}.\n"
        "Run: git clone --depth 1 -b v1.5.2 https://github.com/xiph/opus firmware/components/opus/libopus\n"
        "or set OPUS_SRC_DIR to a libopus checkout.")
endif()

# Read a source list (VAR = a.c \ b.c ...) from one of libopus's .mk files.
function(opus_mk_list mkfile var out)
    file(READ "${OPUS_DIR}/${mkfile}" content)
    string(REGEX REPLACE "\\\\\r?\n" " " content "${content}")
    string(REGEX MATCH "(^|\n)${var} *= *([^\n]*)" _m "${content}")
    set(items "${CMAKE_MATCH_2}")
    separate_arguments(items)
    list(TRANSFORM items PREPEND "${OPUS_DIR}/")
    set(${out} ${items} PARENT_SCOPE)
endfunction()

opus_mk_list(celt_sources.mk CELT_SOURCES celt)
opus_mk_list(silk_sources.mk SILK_SOURCES silk)
opus_mk_list(silk_sources.mk SILK_SOURCES_FLOAT silk_float)
opus_mk_list(opus_sources.mk OPUS_SOURCES opus)
opus_mk_list(opus_sources.mk OPUS_SOURCES_FLOAT opus_float)

idf_component_register(
    SRCS ${celt} ${silk} ${silk_float} ${opus} ${opus_float}
    INCLUDE_DIRS "${OPUS_DIR}/include"
    PRIV_INCLUDE_DIRS "${OPUS_DIR}" "${OPUS_DIR}/celt" "${OPUS_DIR}/silk" "${OPUS_DIR}/silk/float" "${OPUS_DIR}/dnn"
)
target_compile_definitions(${COMPONENT_LIB} PRIVATE OPUS_BUILD VAR_ARRAYS HAVE_LRINT HAVE_LRINTF)
# third-party code: keep the build log quiet and never fail on its warnings
target_compile_options(${COMPONENT_LIB} PRIVATE -w -O2)
